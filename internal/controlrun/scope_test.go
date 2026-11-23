package controlrun

import (
	"context"
	"database/sql"
	"net/url"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/reconcile"
	"github.com/dinoderek/engine/internal/session"

	_ "modernc.org/sqlite" // The driver the plan tests read query plans with.
)

// started dispatches task and has its session claim it.
func started(t *testing.T, c *Coordinator, task control.Item) {
	t.Helper()
	pass(t, c)
	pass(t, c)
	claim(t, c, task)
}

// otherPlan adds a second repository whose plan coordinates, with a task that
// has a session, an open question and a merge request, and returns every ID
// of those records: a pass that reads them for another plan has read too much.
func otherPlan(t *testing.T, c *Coordinator) []string {
	t.Helper()
	repo := execute(t, c.Service, control.Request{Op: "create", Item: control.Item{Kind: "repo", Title: "Other", Slug: "owner/other", Remote: "ssh://example.test/other"}})
	plan := execute(t, c.Service, control.Request{Op: "create", Item: control.Item{Kind: "plan", Title: "Other feature", Repo: repo.ID, Body: "Requirements"}})
	task := execute(t, c.Service, control.Request{Op: "create", Item: control.Item{Kind: "task", Title: "Other task", Repo: repo.ID, Plan: plan.ID, Body: "## Outcome\nWorks\n## Tests\nProve it"}})
	plan = execute(t, c.Service, control.Request{Op: "approve", ID: plan.ID, Version: plan.Version})
	execute(t, c.Service, control.Request{Op: "run", ID: plan.ID, Version: plan.Version})
	mutate(t, c.Service.Store, func(tx *control.Tx) error {
		task, _ = tx.Item(task.ID)
		task.State, task.Attempt = "in-review", "a900"
		tx.PutItem(task)
		tx.PutAttempt(control.Attempt{ID: "a900", Item: task.ID, State: "running", Host: "devbox", Harness: "claude", Window: "@9"})
		q := tx.AppendUpdate(control.Actor{Name: "a900"}, control.Request{ID: task.ID, Recipient: "operator", RequiresReply: true}, "ask", "Which way?", c.now())
		req := tx.AppendUpdate(control.Actor{Name: "a900"}, control.Request{ID: task.ID}, "merge-request", "merge it", c.now())
		tx.PutQueueEntry(control.QueueEntry{Task: task.ID, Repo: repo.ID, Seq: 1, Request: req.ID})
		_ = q
		return nil
	})
	return []string{repo.ID, plan.ID, task.ID, "a900"}
}

// planRead is what a read of one plan's View ran: each query with its plan.
type planRead struct {
	query string
	args  []any
	plan  []string
}

// tracedRead runs fn in a read, and returns every query it ran, with the
// plan SQLite makes for each, read on a second connection to the database.
func tracedRead(t *testing.T, path string, s *control.Store, fn func(*control.Tx)) []planRead {
	t.Helper()
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}).String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	var reads []planRead
	s.Trace(func(query string, args []any) { reads = append(reads, planRead{query: query, args: args}) })
	defer s.Trace(nil)
	inTx(t, s, fn)
	for i, r := range reads {
		rows, err := db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+r.query, r.args...)
		if err != nil {
			t.Fatalf("%s: %v", r.query, err)
		}
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			reads[i].plan = append(reads[i].plan, detail)
		}
		if err = rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return reads
}

// scans are the plan steps that walk a whole table, those of the few rows the
// server keeps (a host's record, the counters) and of what is bounded by what
// is in flight (the merge queue, the attempts that hit a usage limit) aside.
func scans(reads []planRead) []string {
	var out []string
	for _, r := range reads {
		for _, step := range r.plan {
			if strings.HasPrefix(step, "SCAN ") && !strings.Contains(step, "CONSTANT ROW") &&
				!strings.Contains(step, "INDEX attempts_limited") && !strings.Contains(step, "INDEX queue_repo") {
				out = append(out, step+" <- "+r.query)
			}
		}
	}
	return out
}

// Building one plan's View reads only that plan's items, attempts, questions
// and its repository's queue: no query names another plan's records, none
// walks a table, and the View is what a database holding only that plan
// gives.
func TestViewReadsOnlyItsPlansRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	c, _, _, plan, task := setupAt(t, path)
	started(t, c, task)
	alone, _, _, alonePlan, aloneTask := setupAt(t, filepath.Join(t.TempDir(), "db"))
	started(t, alone, aloneTask)
	var want reconcile.View
	inTx(t, alone.Service.Store, func(tx *control.Tx) { want = alone.view(tx, alonePlan.ID, reconcile.Fleet{}) })

	foreign := otherPlan(t, c)
	var got reconcile.View
	reads := tracedRead(t, path, c.Service.Store, func(tx *control.Tx) { got = c.view(tx, plan.ID, reconcile.Fleet{}) })

	if len(reads) == 0 {
		t.Fatal("the View ran no query")
	}
	for _, r := range reads {
		for _, arg := range r.args {
			for _, id := range foreign {
				if arg == id {
					t.Errorf("a View of %s read %v: %s", plan.ID, id, r.query)
				}
			}
		}
	}
	if walked := scans(reads); len(walked) > 0 {
		t.Errorf("the View scans tables: %v", walked)
	}
	if !reflect.DeepEqual(got.Root, want.Root) || !reflect.DeepEqual(got.Tasks, want.Tasks) || !reflect.DeepEqual(got.Lint, want.Lint) {
		t.Errorf("another plan changed the View\n got %+v\nwant %+v", got.Tasks, want.Tasks)
	}
	if got.Tasks[0].Current.ID == "" || got.Tasks[0].Session == nil || !got.Tasks[0].Claimed {
		t.Errorf("the fixture's task has no claimed session: %+v", got.Tasks[0])
	}
}

// A whole pass, merge queues, dispatches and decisions included, walks no
// table of records: each read is indexed by a plan, an item, an attempt, a
// repository or a state.
func TestAPassWalksNoTableOfRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	c, h, _, _, task := setupAt(t, path)
	started(t, c, task)
	h.status.Windows[0].State = session.StateWaiting
	foreign := otherPlan(t, c)
	// A usage limit another plan's session hit is the whole fleet's to know,
	// and a session of this plan has something to be woken for.
	mutate(t, c.Service.Store, func(tx *control.Tx) error {
		tx.PutAttempt(control.Attempt{ID: "a901", Item: foreign[2], State: "ended", Host: "devbox", Harness: "claude", Limited: true})
		return nil
	})
	c.Wake = func(reconcile.Session, string) error { return nil }
	say(t, c, task)
	reads := tracedRead(t, path, c.Service.Store, func(*control.Tx) {
		pass(t, c)
	})
	if len(reads) == 0 {
		t.Fatal("the pass ran no query")
	}
	if walked := scans(reads); len(walked) > 0 {
		t.Errorf("a pass scans tables: %v", walked)
	}
	for _, want := range []string{"SELECT DISTINCT a.host", "AND a.host=? AND json_extract", "FROM updates WHERE id>? AND item=?", "SELECT DISTINCT repo FROM queue"} {
		if !slices.ContainsFunc(reads, func(r planRead) bool { return strings.Contains(r.query, want) }) {
			t.Errorf("the pass never ran a query with %q", want)
		}
	}
}

// say has the operator tell task's session something.
func say(t *testing.T, c *Coordinator, task control.Item) {
	t.Helper()
	_, err := c.Service.Execute(control.Actor{Name: "operator", Role: "operator"}, control.Request{Op: "say", ID: task.ID, Body: "carry on", Key: control.Secret()})
	if err != nil {
		t.Fatal(err)
	}
}
