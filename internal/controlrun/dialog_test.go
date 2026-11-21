package controlrun

import (
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/reconcile"
	"github.com/dinoderek/engine/internal/session"
)

// A planner held at a dialog before its session started is never typed
// into, since Enter would take the dialog's default, and its stall is
// posted once StallWait has passed, saying to answer the dialog.
func TestAPlannerHeldAtADialogStallsAndIsNeverTyped(t *testing.T) {
	c, h, _, plan, task := setup(t)
	if err := c.Service.Store.Transaction(func(st *control.State) error { delete(st.Items, task.ID); return nil }); err != nil {
		t.Fatal(err)
	}
	pass(t, c)
	if len(h.status.Windows) != 1 {
		t.Fatalf("windows %v, want the planner's", h.status.Windows)
	}
	h.status.Windows[0].State, h.status.Windows[0].Dialog = session.StateWaiting, true
	woke := 0
	c.Wake = func(reconcile.Session, string) error { woke++; return nil }
	execute(t, c.Service, control.Request{Op: "say", ID: plan.ID, Body: "Also cover the edge case"})
	now := c.Service.Now()
	pass(t, c)
	if n := dialogNotices(t, c, plan.ID); len(n) != 0 {
		t.Fatalf("notices before StallWait: %q", n)
	}
	later := now.Add(reconcile.StallWait)
	c.Service.Now = func() time.Time { return later }
	pass(t, c)
	pass(t, c)
	if n := dialogNotices(t, c, plan.ID); len(n) != 1 || !strings.Contains(n[0], "has waited for input for 10m0s: its harness says it is likely held at a dialog in its window") {
		t.Errorf("notices %q, want one stall naming the dialog", n)
	}
	if woke != 0 {
		t.Errorf("typed into a session at a dialog %d times", woke)
	}
}

func dialogNotices(t *testing.T, c *Coordinator, id string) []string {
	t.Helper()
	st, err := c.Service.Store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, u := range st.Updates {
		if u.Item == id && strings.Contains(u.Body, "waited for input") {
			out = append(out, u.Body)
		}
	}
	return out
}

// A session at a dialog is not waiting for typed input, but its wait is
// timed, and the host reads it as waiting.
func TestAWindowAtADialogIsTimedButNotIdle(t *testing.T) {
	var s reconcile.Session
	withWindow(&s, fakeWindow(session.StateWaiting, true))
	if !s.Dialog || s.Waiting || s.Idle() || !s.AtDialog() {
		t.Fatalf("session %+v: want at a dialog, not waiting or idle", s)
	}
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	if at := kept(control.Attempt{}, s, now, true); !at.WaitingSince.Equal(now) || !at.IdleSince.IsZero() {
		t.Errorf("kept waiting %v, idle %v; want the wait timed and no idle spell", at.WaitingSince, at.IdleSince)
	}
	var plain reconcile.Session
	withWindow(&plain, fakeWindow(session.StateWaiting, false))
	if plain.Dialog || !plain.Waiting {
		t.Errorf("a plain wait: %+v", plain)
	}
}

func fakeWindow(state string, dialog bool) hostops.Window {
	return hostops.Window{Name: sessionName("owner/demo", 1), ID: "@1", Alive: true, State: state, Dialog: dialog}
}

// The coordinator's own reading of a session at a dialog is waiting.
func TestHostStateOfADialogIsWaiting(t *testing.T) {
	at := control.Attempt{Host: "devbox", Window: "@1"}
	h := reconcile.HostLoad{Name: "devbox", Status: hostops.Status{Windows: []hostops.Window{fakeWindow(session.StateWaiting, true)}}}
	if got := hostState(at, h, "owner/demo", 1); got != session.StateWaiting {
		t.Errorf("host state %q, want waiting", got)
	}
}
