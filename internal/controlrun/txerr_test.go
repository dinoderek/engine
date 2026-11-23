package controlrun

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/reconcile"
	"github.com/dinoderek/engine/internal/session"
)

// TestAPassActsOnNoViewAQueryFailedToRead corrupts an update only the pass's
// view reads: the pass must fail before deciding, never dispatch on what it
// could not read.
func TestAPassActsOnNoViewAQueryFailedToRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	c, h, _, _, task := setupAt(t, path)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err = db.Exec("UPDATE updates SET requires_reply=1, data='not json' WHERE item=?", task.ID); err != nil {
		t.Fatal(err)
	}
	if err = c.Pass(context.Background()); err == nil {
		t.Fatal("a pass over a corrupt update succeeded")
	}
	if h.calls != 0 || len(h.launches) != 0 {
		t.Fatalf("the pass acted: %d host calls, launches %v", h.calls, h.launches)
	}
	// A view that fails to read part of the records is never decided on.
	err = read(c.Service.Store, func(tx *control.Tx) error {
		if err := c.decideAndApply(context.Background(), tx, task.Plan, c.fleet(c.Config, c.Host), c.Config); err == nil {
			t.Error("a pass decided on a view it failed to read")
		}
		return nil
	})
	if err == nil {
		t.Fatal("a failed view returned no error")
	}
	// Deciding on a Tx that already failed returns its error instead of acting.
	err = read(c.Service.Store, func(tx *control.Tx) error {
		for range tx.QuestionsAbout(task.ID) {
		}
		if tx.Err() == nil {
			t.Fatal("a corrupt question read")
		}
		if err := c.decideAndApply(context.Background(), tx, task.Plan, reconcile.Fleet{}, c.Config); err == nil {
			t.Error("a pass decided on a failed read")
		}
		return nil
	})
	if err == nil {
		t.Fatal("a failed read returned no error")
	}
}

// TestRecoveryStopsAtAnAttemptItFailedToRead corrupts the middle one of three
// dispatching attempts: recovery adopts the first and, every read after a
// failed one finding nothing, leaves the last alone.
func TestRecoveryStopsAtAnAttemptItFailedToRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	store, err := control.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	items := []control.Item{{ID: "r1", Kind: "repo", Slug: "owner/demo"},
		{ID: "p2", Kind: "plan", Repo: "r1", State: "planned", ApprovedVersion: 1, Coordinating: true}}
	var attempts []control.Attempt
	var runs []hostops.Run
	for _, n := range []string{"3", "4", "5"} {
		items = append(items, control.Item{ID: "t" + n, Kind: "task", Repo: "r1", Plan: "p2", State: "running", Attempt: "a" + n})
		attempts = append(attempts, control.Attempt{ID: "a" + n, Item: "t" + n, Host: "devbox", Harness: "claude", State: "dispatching", RunID: "run-" + n})
		runs = append(runs, hostops.Run{Name: "demo-" + n, Record: session.Record{RunID: "run-" + n, Tmux: "engine:@" + n}})
	}
	mutate(t, store, func(tx *control.Tx) error {
		for _, item := range items {
			tx.PutItem(item)
		}
		for _, at := range attempts {
			tx.PutAttempt(at)
		}
		return nil
	})
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err = db.Exec("UPDATE attempts SET data='not json' WHERE id='a4'"); err != nil {
		t.Fatal(err)
	}
	c := &Coordinator{Service: control.Service{Store: store}, Logf: t.Logf}
	fleet := reconcile.Fleet{Hosts: []reconcile.HostLoad{{Name: "devbox", Status: hostops.Status{Runs: runs}}}}
	if err = c.reconcileDispatches("p2", fleet, &hosts.Config{}); err == nil {
		t.Fatal("recovery over a corrupt attempt succeeded")
	}
	var first, last control.Attempt
	inTx(t, store, func(tx *control.Tx) { first, _ = tx.Attempt("a3"); last, _ = tx.Attempt("a5") })
	if first.State != "running" || last.State != "dispatching" {
		t.Fatalf("a3 %s, a5 %s; want the first adopted and the last left dispatching", first.State, last.State)
	}
}

// TestALaunchNeverSpawnsOnARecordItFailedToRead reserves a dispatch, then
// corrupts its repository: the launch reads it for the spawn and must stop.
func TestALaunchNeverSpawnsOnARecordItFailedToRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	c, h, repo, _, task := setupAt(t, path)
	out, err := c.Service.Dispatch(task, "devbox", "claude", "reserve")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err = db.Exec("UPDATE items SET data='not json' WHERE id=?", repo.ID); err != nil {
		t.Fatal(err)
	}
	err = read(c.Service.Store, func(tx *control.Tx) error {
		if err := c.launch(tx, *out.Attempt, out.Credential, false, c.Config); err == nil {
			t.Error("a launch over a corrupt repository succeeded")
		}
		return nil
	})
	if err == nil || len(h.launches) != 0 {
		t.Fatalf("read error %v, launches %v; want a failed read and no spawn", err, h.launches)
	}
}
