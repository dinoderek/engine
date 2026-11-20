package controlrun

import (
	"errors"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/session"
)

func TestRecoveryKeyTellsErrorsApartByTheirFirstLine(t *testing.T) {
	a := recoveryKey("a3", errors.New("ssh: broken pipe\nretry 1"))
	if b := recoveryKey("a3", errors.New("ssh: broken pipe\nretry 2")); a != b {
		t.Errorf("the same first line gave %s and %s", a, b)
	}
	if b := recoveryKey("a3", errors.New("disk full")); a == b || !strings.HasPrefix(b, "dispatch/a3/") || len(b) != len("dispatch/a3/")+12 {
		t.Errorf("a different error gave %s, beside %s", b, a)
	}
	if b := recoveryKey("a4", errors.New("ssh: broken pipe")); a == b {
		t.Error("another attempt shared the key")
	}
}

func unresolvedNotices(t *testing.T, c *Coordinator, task control.Item) int {
	t.Helper()
	st, _ := c.Service.Store.Snapshot()
	n := 0
	for _, u := range st.Updates {
		if u.Item == task.ID && strings.Contains(u.Body, "unresolved outcome") {
			n++
		}
	}
	return n
}

// Each distinct recovery error posts one notice; its repeats post nothing.
func TestEachDistinctRecoveryErrorPostsOnce(t *testing.T) {
	c, h, _, _, task := setup(t)
	h.unresolved = true
	for range 3 {
		pass(t, c)
	}
	if n := unresolvedNotices(t, c, task); n != 1 {
		t.Fatalf("one error repeated over three passes posted %d notices", n)
	}
	h.unresolvedErr = "ssh: broken pipe\nfirst detail"
	pass(t, c)
	h.unresolvedErr = "ssh: broken pipe\nsecond detail"
	pass(t, c)
	if n := unresolvedNotices(t, c, task); n != 2 {
		t.Fatalf("a second distinct error posted %d notices in all, want 2", n)
	}
}

// The operator's reconcile ends a dispatch the host never launched, so the
// task returns to todo and the next pass dispatches a new attempt.
func TestReconcileEndsADispatchTheHostNeverLaunched(t *testing.T) {
	c, h, _, _, task := setup(t)
	c.Service.Reconcile = c.ConfirmEnded
	h.unresolved = true
	pass(t, c)
	pass(t, c)
	st, _ := c.Service.Store.Snapshot()
	stuck := st.Attempts[st.Items[task.ID].Attempt]
	if stuck.State != "dispatching" {
		t.Fatalf("the unresolved dispatch is %s", stuck.State)
	}
	out, err := c.Service.Execute(control.Actor{Name: "operator", Role: "operator"},
		control.Request{Op: "attempt-reconcile", ID: stuck.ID, Body: "the host shows nothing", Key: "reconcile"})
	if err != nil {
		t.Fatal(err)
	}
	st, _ = c.Service.Store.Snapshot()
	if out.Attempt.Reason != control.EndVanished || st.Items[task.ID].State != "todo" {
		t.Fatalf("the reconciled dispatch: %+v, task %s", out.Attempt, st.Items[task.ID].State)
	}
	h.unresolved = false
	pass(t, c)
	st, _ = c.Service.Store.Snapshot()
	next := st.Attempts[st.Items[task.ID].Attempt]
	if next.ID == stuck.ID || next.State != "running" {
		t.Fatalf("no new attempt was dispatched: %+v", next)
	}
	// The new attempt is no resume of the unlaunched one: seen live with a
	// PR, it stays resumable when its window goes.
	pass(t, c)
	worker, err := c.Service.Authenticate(h.launches[next.RunID].Credential, "operator")
	if err != nil {
		t.Fatal(err)
	}
	st, _ = c.Service.Store.Snapshot()
	if _, err = c.Service.Execute(worker, control.Request{Op: "state", ID: task.ID, Version: st.Items[task.ID].Version, State: "in-review",
		Item: control.Item{PR: reviewedPR}, Key: "review"}); err != nil {
		t.Fatal(err)
	}
	h.status.Windows = nil
	pass(t, c)
	st, _ = c.Service.Store.Snapshot()
	if at := st.Attempts[next.ID]; at.Reason != control.EndVanished || st.Items[task.ID].State == "failed" {
		t.Fatalf("the attempt after an unlaunched one lost its resume: %+v, task %s", at, st.Items[task.ID].State)
	}
}

// A dispatching attempt the host may still be launching, or that it cannot
// read, is refused with a 409 problem of its own reason.
func TestReconcileRefusesADispatchTheHostMayHaveLaunched(t *testing.T) {
	for name, edit := range map[string]func(*host, string){
		"a window of its name": func(h *host, _ string) {
			h.status.Windows = []hostops.Window{{Name: "demo-3", ID: "@4"}}
		},
		"a run record": func(h *host, run string) {
			h.status.Runs = []hostops.Run{{Name: "demo-3", Record: session.Record{RunID: run}}}
		},
		"an unreadable host": func(h *host, _ string) { h.outage = true },
		"unreadable runs":    func(h *host, _ string) { h.status.RunsError = "run.json: bad" },
	} {
		t.Run(name, func(t *testing.T) {
			c, h, _, _, task := setup(t)
			h.unresolved = true
			pass(t, c)
			pass(t, c)
			st, _ := c.Service.Store.Snapshot()
			at := st.Attempts[st.Items[task.ID].Attempt]
			if task.ID != "t3" || at.State != "dispatching" {
				t.Fatalf("want task t3's attempt dispatching, got %s's %s", task.ID, at.State)
			}
			edit(h, at.RunID)
			err := c.ConfirmEnded(at.ID)
			var p *control.Problem
			if !errors.As(err, &p) || p.Status != 409 || p.Reason != control.ReasonReconcileRefused {
				t.Fatalf("err = %v, want a 409 reconcile refusal", err)
			}
		})
	}
}
