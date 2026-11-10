package control

import (
	"errors"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

var op = Actor{Name: "operator", Role: "operator"}

func service(t *testing.T) Service {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 is absent")
	}
	store, err := Open(filepath.Join(t.TempDir(), "control.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	return Service{Store: store, Now: func() time.Time { return time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC) }}
}

func call(t *testing.T, s Service, a Actor, r Request) Response {
	t.Helper()
	if r.Key == "" {
		r.Key = Secret()
	}
	out, err := s.Execute(a, r)
	if err != nil {
		t.Fatalf("%s: %v", r.Op, err)
	}
	return out
}

func reject(t *testing.T, s Service, a Actor, r Request, status int) {
	t.Helper()
	if r.Key == "" {
		r.Key = Secret()
	}
	_, err := s.Execute(a, r)
	var p *Problem
	if !errors.As(err, &p) || p.Status != status {
		t.Fatalf("%s: got %v, want HTTP %d", r.Op, err, status)
	}
}

func repoPlan(t *testing.T, s Service) (Item, Item) {
	t.Helper()
	repo := *call(t, s, op, Request{Op: "create", Item: Item{Kind: "repo", Title: "Demo", Slug: "owner/demo", Remote: "git@example.test:owner/demo.git"}}).Item
	plan := *call(t, s, op, Request{Op: "create", Item: Item{Kind: "plan", Repo: repo.ID, Title: "A feature", Body: "Agreed requirements"}}).Item
	return repo, plan
}

func newTask(t *testing.T, s Service, repo, plan string) Item {
	t.Helper()
	return *call(t, s, op, Request{Op: "create", Item: Item{Kind: "task", Repo: repo, Plan: plan, Title: "Build", Body: "## Outcome\nWorks\n## Tests\nProve it"}}).Item
}

func approve(t *testing.T, s Service, item Item) Item {
	t.Helper()
	return *call(t, s, op, Request{Op: "approve", ID: item.ID, Version: item.Version}).Item
}

func dispatch(t *testing.T, s Service, item Item) (Actor, Response) {
	t.Helper()
	out := call(t, s, op, Request{Op: "claim", ID: item.ID, Body: "devbox", State: "claude"})
	a, err := s.Authenticate(out.Credential, "operator-secret")
	if err != nil {
		t.Fatal(err)
	}
	return a, out
}

func TestPlanLifecycleAndVersionedEdits(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	task := newTask(t, s, repo.ID, plan.ID)
	reject(t, s, op, Request{Op: "run", ID: plan.ID, Version: plan.Version}, 409)
	plan = approve(t, s, plan)
	plan = *call(t, s, op, Request{Op: "run", ID: plan.ID, Version: plan.Version}).Item
	if !plan.Coordinating {
		t.Fatal("run intent was not persisted")
	}
	edited := Item{Title: "Updated", Body: "Revised requirements"}
	reject(t, s, op, Request{Op: "edit", ID: plan.ID, Version: 1, Item: edited}, 409)
	plan = *call(t, s, op, Request{Op: "edit", ID: plan.ID, Version: plan.Version, Item: edited}).Item
	if plan.ApprovedVersion != 0 || plan.Coordinating || plan.State != "prepare" {
		t.Fatalf("edit retained approval: %+v", plan)
	}
	plan = approve(t, s, plan)
	plan = *call(t, s, op, Request{Op: "state", ID: plan.ID, Version: plan.Version, State: "planned"}).Item
	reject(t, s, op, Request{Op: "state", ID: plan.ID, Version: plan.Version, State: "done"}, 409)
	a, out := dispatch(t, s, task)
	task = *out.Item
	reject(t, s, a, Request{Op: "edit", ID: task.ID, Version: task.Version, Item: edited}, 409)
	reject(t, s, a, Request{Op: "approve", ID: task.ID, Version: task.Version}, 400)
	task = *call(t, s, a, Request{Op: "state", ID: task.ID, Version: task.Version, State: "in-review", Item: Item{PR: "https://example.test/pr"}}).Item
	task = *call(t, s, a, Request{Op: "state", ID: task.ID, Version: task.Version, State: "done"}).Item
	if task.PR == "" {
		t.Fatal("PR pointer not recorded")
	}
	plan = *call(t, s, op, Request{Op: "state", ID: plan.ID, Version: plan.Version, State: "done"}).Item
	if plan.State != "done" {
		t.Fatal(plan)
	}
	if err := s.SetAttempt(out.Attempt.ID, "ended", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(out.Credential, ""); err == nil {
		t.Fatal("ended execution credential accepted")
	}
	reject(t, s, a, Request{Op: "say", ID: task.ID, Body: "stale"}, 403)
	items := call(t, s, op, Request{Op: "list", State: "task", ID: plan.ID}).Items
	if len(items) != 1 || items[0].ID != task.ID {
		t.Fatal(items)
	}
	if len(call(t, s, op, Request{Op: "attempts", ID: task.ID}).Attempts) != 1 {
		t.Fatal("attempt missing")
	}
}

func TestDependenciesAndAuthorization(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	one := newTask(t, s, repo.ID, plan.ID)
	two := *call(t, s, op, Request{Op: "create", Item: Item{Kind: "task", Repo: repo.ID, Plan: plan.ID, Title: "Dependent", Depends: []string{one.ID}}}).Item
	plan = approve(t, s, plan)
	reject(t, s, op, Request{Op: "claim", ID: two.ID, Body: "devbox", State: "claude"}, 409)
	reject(t, s, op, Request{Op: "edit", ID: one.ID, Version: one.Version, Item: Item{Title: "Cycle", Depends: []string{two.ID}}}, 400)
	a, out := dispatch(t, s, one)
	reject(t, s, a, Request{Op: "state", ID: two.ID, Version: two.Version, State: "running"}, 403)
	reject(t, s, a, Request{Op: "create", Item: Item{Kind: "repo", Title: "Repo", Slug: "x/y", Remote: "remote"}}, 403)
	reject(t, s, a, Request{Op: "create", Item: Item{Kind: "task", Repo: repo.ID, Plan: plan.ID, Title: "Sibling"}}, 403)
	reject(t, s, op, Request{Op: "claim", ID: one.ID, Body: "devbox", State: "claude"}, 409)
	call(t, s, a, Request{Op: "state", ID: one.ID, Version: out.Item.Version, State: "failed"})
	reject(t, s, a, Request{Op: "state", ID: one.ID, Version: out.Item.Version + 1, State: "todo"}, 403)
	if err := s.SetAttempt(out.Attempt.ID, "ended", ""); err != nil {
		t.Fatal(err)
	}
	call(t, s, op, Request{Op: "state", ID: one.ID, Version: out.Item.Version + 1, State: "todo"})
}

func TestPlannerCanCreateTasksButCannotApprove(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	plan = approve(t, s, plan)
	a, _ := dispatch(t, s, plan)
	task := call(t, s, a, Request{Op: "create", Item: Item{Kind: "task", Repo: repo.ID, Plan: plan.ID, Title: "Planned task"}}).Item
	if task.Plan != plan.ID {
		t.Fatal(task)
	}
	reject(t, s, a, Request{Op: "approve", ID: plan.ID, Version: plan.Version + 1}, 403)
	reject(t, s, a, Request{Op: "create", Item: Item{Kind: "plan", Repo: repo.ID, Title: "Other plan"}}, 403)
}

func TestRequestDeduplicationAndConcurrentClaims(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	task := newTask(t, s, repo.ID, plan.ID)
	approve(t, s, plan)
	call(t, s, op, Request{Op: "run", ID: plan.ID})
	r := Request{Op: "say", ID: task.ID, Body: "hello", Key: "same"}
	first := call(t, s, op, r)
	second := call(t, s, op, r)
	if first.Updates[0].ID != second.Updates[0].ID {
		t.Fatal("duplicate message")
	}
	r.Body = "different"
	reject(t, s, op, r, 409)
	var mu sync.Mutex
	successes := 0
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			_, err := s.Dispatch(task, "devbox", "claude", Secret())
			if err == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if successes != 1 {
		t.Fatalf("%d claims succeeded", successes)
	}
	st, err := s.Store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Attempts) != 1 || len(st.ReceiptsForDispatch(st.Items[task.ID].Attempt)) != 1 {
		t.Fatal("dispatch identity missing")
	}
}

func TestInvalidCommandsDoNotChangeHistory(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	before, _ := s.Store.Snapshot()
	cases := []Request{
		{Op: "unknown"}, {Op: "create", Item: Item{Kind: "task", Repo: repo.ID}},
		{Op: "create", Item: Item{Kind: "unknown", Title: "x"}},
		{Op: "create", Item: Item{Kind: "repo", Title: "x", Slug: "../x", Remote: "x"}},
		{Op: "create", Item: Item{Kind: "repo", Title: "x", Slug: "owner/demo", Remote: "x"}},
		{Op: "create", Item: Item{Kind: "task", Title: "x", Repo: "missing"}},
		{Op: "create", Item: Item{Kind: "task", Title: "x", Repo: repo.ID, Plan: "missing"}},
		{Op: "create", Item: Item{Kind: "task", Title: "x", Repo: repo.ID, State: "done"}},
		{Op: "create", Item: Item{Kind: "task", Title: "x", Repo: repo.ID, Depends: []string{"missing"}}},
		{Op: "create", Item: Item{Kind: "task", Title: "x", Repo: repo.ID, Tier: "bad"}},
		{Op: "edit", ID: repo.ID, Version: repo.Version, Item: Item{Title: "x"}},
		{Op: "edit", ID: plan.ID, Version: plan.Version, Item: Item{}},
	}
	for _, r := range cases {
		t.Run(r.Op+"/"+r.Item.Kind+"/"+r.Item.Title, func(t *testing.T) {
			r.Key = Secret()
			if _, err := s.Execute(op, r); err == nil {
				t.Fatal("invalid command accepted")
			}
		})
	}
	after, _ := s.Store.Snapshot()
	if len(before.Updates) != len(after.Updates) || len(before.Items) != len(after.Items) {
		t.Fatal("failed transaction changed state")
	}
	if Number("bad") != 0 || Number("t123") != 123 || !strings.HasPrefix(Secret(), "") {
		t.Fatal("identity parser")
	}
	_, err := s.Execute(op, Request{Op: "say", Body: "missing key"})
	if err == nil {
		t.Fatal("missing request key accepted")
	}
	reject(t, s, op, Request{Op: "get", ID: "absent"}, 404)
	reject(t, s, op, Request{Op: "edit", ID: "absent"}, 404)
	reject(t, s, op, Request{Op: "state", ID: "absent"}, 404)
	reject(t, s, op, Request{Op: "state", ID: repo.ID, Version: repo.Version, State: "running"}, 400)
}

// Every task belongs to a plan: a task without one is refused, and only its
// plan is approved, run or stopped.
func TestATaskRequiresAPlan(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	before := mustSnapshot(t, s)
	reject(t, s, op, Request{Op: "create", Item: Item{Kind: "task", Repo: repo.ID, Title: "Alone", Body: "## Outcome\nWorks"}}, 400)
	if !reflect.DeepEqual(before, mustSnapshot(t, s)) {
		t.Fatal("a refused task changed storage")
	}
	task := newTask(t, s, repo.ID, plan.ID)
	if task.State != "todo" {
		t.Fatalf("a new task is %s, want todo", task.State)
	}
	for _, o := range []string{"approve", "run", "stop"} {
		reject(t, s, op, Request{Op: o, ID: task.ID, Version: task.Version}, 400)
	}
	plan = approve(t, s, plan)
	plan = *call(t, s, op, Request{Op: "run", ID: plan.ID, Version: plan.Version}).Item
	edited := *call(t, s, op, Request{Op: "edit", ID: task.ID, Version: task.Version, Item: Item{Title: "Rebuilt"}}).Item
	if edited.State != "todo" || edited.ApprovedVersion != 0 || edited.Coordinating {
		t.Fatalf("a task edit changed its state: %+v", edited)
	}
	if p := call(t, s, op, Request{Op: "get", ID: plan.ID}).Item; !p.Coordinating || p.ApprovedVersion == 0 {
		t.Fatalf("a task edit changed its plan's approval: %+v", p)
	}
}
