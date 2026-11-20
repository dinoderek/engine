package controlrun

import (
	"errors"
	"fmt"
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
	// Each try opens a new window, so the same respawn failure names new ids.
	respawn := "open the tmux window: respawn-pane failed, and again after 1s: first: tmux respawn-pane -k -t %%%d: exit status 1; then: tmux respawn-pane -k -t %%%d @%d\npanes"
	first := recoveryKey("a3", fmt.Errorf(respawn, 12, 12, 7))
	if again := recoveryKey("a3", fmt.Errorf(respawn, 13, 13, 8)); first != again {
		t.Errorf("one failure on new tmux ids gave %s and %s", first, again)
	}
	if other := recoveryKey("a3", errors.New("open the tmux window: no server running")); first == other {
		t.Error("a different tmux failure shared the key")
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
			h.status.Runs = []hostops.Run{{Name: "demo-3", Record: session.Record{RunID: run, Tmux: "engine:@4"}}}
		},
		"an incomplete run record": func(h *host, run string) {
			h.status.Runs = []hostops.Run{{Name: "demo-3", Dir: "/runs/" + run, Record: session.Record{RunID: run, Tmux: "engine:"}}}
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
			if name == "an incomplete run record" && !strings.Contains(err.Error(), "remove /runs/"+at.RunID+" on the host and reconcile again") {
				t.Errorf("the refusal names no remedy: %v", err)
			}
		})
	}
}

// A planner's dispatch keeps retrying each pass; ending it would leave the
// plan with no planner and nothing to dispatch one again.
func TestReconcileRefusesAPlannersDispatch(t *testing.T) {
	c, h, _, plan, _ := setup(t)
	st, _ := c.Service.Store.Snapshot()
	out, err := c.Service.Dispatch(st.Items[plan.ID], "devbox", "claude", "planner")
	if err != nil {
		t.Fatal(err)
	}
	err = c.ConfirmEnded(out.Attempt.ID)
	var p *control.Problem
	if !errors.As(err, &p) || p.Status != 409 || p.Reason != control.ReasonReconcileRefused || !strings.Contains(err.Error(), "planner") {
		t.Fatalf("err = %v, want a 409 reconcile refusal", err)
	}
	if h.calls != 0 {
		t.Errorf("the refusal called the host %d times", h.calls)
	}
}
