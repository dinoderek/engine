package control

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanStatus(t *testing.T) {
	plan := func(state string) Item { return Item{ID: "p1", Kind: "plan", State: state} }
	tasks := func(states ...string) []Item {
		var out []Item
		for _, s := range states {
			out = append(out, Item{Kind: "task", Plan: "p1", State: s})
		}
		return out
	}
	for _, tc := range []struct {
		name  string
		plan  Item
		tasks []Item
		asked bool
		want  string
	}{
		{"prepare is stored", plan("prepare"), nil, false, "prepare"},
		{"approved while planning", plan("approved"), nil, false, "approved"},
		{"a plan question blocks planning", plan("approved"), nil, true, "blocked"},
		{"planned with every task todo", plan("planned"), tasks("todo", "todo"), false, "planned"},
		{"a started task makes it executing", plan("planned"), tasks("done", "todo"), false, "executing"},
		{"in review is executing", plan("planned"), tasks("in-review"), false, "executing"},
		{"a hard stop blocks it", plan("planned"), tasks("running", "needs-human"), false, "blocked"},
		{"a failed task blocks it", plan("planned"), tasks("failed", "todo"), false, "blocked"},
		{"a plan question blocks it", plan("planned"), tasks("todo"), true, "blocked"},
		{"the acceptance task alone is still executing", plan("planned"), tasks("done", "done", "running"), false, "executing"},
		{"done stays done", plan("done"), tasks("needs-human"), true, "done"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := PlanStatus(tc.plan, tc.tasks, tc.asked); got != tc.want {
				t.Fatalf("PlanStatus = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReadsCarryPlanStatusThatIsNeverStored(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	task := newTask(t, s, repo.ID, plan.ID)
	reject(t, s, op, Request{Op: "create", Item: Item{Kind: "plan", Repo: repo.ID, Title: "Forged", Status: "executing"}}, 400)
	plan = approve(t, s, plan)
	plan = *call(t, s, op, Request{Op: "state", ID: plan.ID, Version: plan.Version, State: "planned"}).Item
	for _, removed := range []string{"executing", "verifying", "blocked"} {
		reject(t, s, op, Request{Op: "state", ID: plan.ID, Version: plan.Version, State: removed}, 409)
	}
	status := func() string {
		t.Helper()
		got := call(t, s, op, Request{Op: "get", ID: plan.ID}).Item.Status
		listed := call(t, s, op, Request{Op: "list", State: "plan"}).Items[0].Status
		viewed := call(t, s, op, Request{Op: "plan-view", ID: plan.ID}).Items[0].Status
		if got != listed || got != viewed {
			t.Fatalf("get %q, list %q, plan-view %q disagree", got, listed, viewed)
		}
		return got
	}
	if got := status(); got != "planned" {
		t.Fatalf("status before dispatch = %q", got)
	}
	worker, _ := dispatch(t, s, task)
	if got := status(); got != "executing" {
		t.Fatalf("status after dispatch = %q", got)
	}
	call(t, s, worker, Request{Op: "ask", ID: plan.ID, Body: "May the feature change its API?"})
	if got := status(); got != "blocked" {
		t.Fatalf("status with a plan question = %q", got)
	}
	if got := call(t, s, op, Request{Op: "get", ID: task.ID}).Item.Status; got != "" {
		t.Fatalf("a task carries a status: %q", got)
	}
	out, err := s.Store.sql("SELECT data FROM records WHERE key LIKE 'item/%';")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "status") {
		t.Fatalf("a stored record carries a status: %s", out)
	}
}

func TestStoreMigratesRemovedPlanStates(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 is absent")
	}
	path := filepath.Join(t.TempDir(), "control.db")
	records := map[string]Item{
		"item/r1": {ID: "r1", Kind: "repo", Version: 1, Title: "Demo", Slug: "o/d", Remote: "x", State: "active"},
		"item/p2": {ID: "p2", Kind: "plan", Version: 7, Repo: "r1", Title: "Executing", State: "executing", ApprovedVersion: 2},
		"item/p3": {ID: "p3", Kind: "plan", Version: 4, Repo: "r1", Title: "Verifying", State: "verifying", ApprovedVersion: 2},
		"item/p4": {ID: "p4", Kind: "plan", Version: 5, Repo: "r1", Title: "Blocked", State: "blocked", ApprovedVersion: 2},
		"item/p5": {ID: "p5", Kind: "plan", Version: 3, Repo: "r1", Title: "Planned", State: "planned", ApprovedVersion: 2},
		"item/t6": {ID: "t6", Kind: "task", Version: 2, Repo: "r1", Plan: "p4", Title: "Stopped", State: "needs-human"},
	}
	var sql strings.Builder
	sql.WriteString("CREATE TABLE records (key TEXT PRIMARY KEY, data TEXT NOT NULL);\n")
	for key, item := range records {
		b, _ := json.Marshal(item)
		sql.WriteString("INSERT INTO records VALUES ('" + key + "', '" + string(b) + "');\n")
	}
	sql.WriteString("PRAGMA user_version=1;\n")
	if _, err := SQLite(context.Background(), path, sql.String()); err != nil {
		t.Fatal(err)
	}
	before := rawRecords(t, path)
	for range 2 { // the second open is a no-op
		store, err := Open(path, nil)
		if err != nil {
			t.Fatal(err)
		}
		st, err := store.Snapshot()
		_ = store.Close()
		if err != nil {
			t.Fatal(err)
		}
		for key, item := range records {
			want := item
			if want.Kind == "plan" {
				want.State = "planned"
			}
			if got := st.Items[want.ID]; got.State != want.State || got.Version != want.Version {
				t.Fatalf("%s migrated to %+v", key, got)
			}
		}
		after := rawRecords(t, path)
		for _, key := range []string{"item/r1", "item/p5", "item/t6"} {
			if after[key] != before[key] {
				t.Fatalf("%s changed: %s, was %s", key, after[key], before[key])
			}
		}
	}
	out, err := SQLite(context.Background(), path, "PRAGMA user_version;")
	if err != nil || !strings.Contains(string(out), `"user_version":2`) {
		t.Fatalf("schema version: %s %v", out, err)
	}
}

func rawRecords(t *testing.T, path string) map[string]string {
	t.Helper()
	out, err := SQLite(context.Background(), path, "SELECT key, data FROM records;")
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct{ Key, Data string }
	if err = json.Unmarshal(out, &rows); err != nil {
		t.Fatal(err)
	}
	raw := map[string]string{}
	for _, r := range rows {
		raw[r.Key] = r.Data
	}
	return raw
}
