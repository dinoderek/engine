package controlrun

import (
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/reconcile"
	"github.com/dinoderek/engine/internal/session"
)

// An executor that waits for its merge in a background job is still woken
// by the merge, and its window, still busy only in that job, is released
// once the report window has passed, with a notice.
func TestASessionBusyOnlyInABackgroundJobIsWokenAndReleased(t *testing.T) {
	c, m, task, at := standaloneMergeRequest(t)
	m.status.Windows[0].State, m.status.Windows[0].Background = session.StateWorking, true
	var texts []string
	c.Wake = func(_ reconcile.Session, text string) error { texts = append(texts, text); return nil }
	pass(t, c)
	st, _ := c.Service.Store.Snapshot()
	if st.Items[task.ID].State != "done" || len(m.status.Windows) != 1 {
		t.Fatalf("task %s, windows %v: want merged with the window kept to report", st.Items[task.ID].State, m.status.Windows)
	}
	if len(texts) != 1 || !strings.Contains(texts[0], "engine updates "+task.ID+" --after") {
		t.Fatalf("woke with %q, want one pointer at the merge", texts)
	}
	pass(t, c)
	if len(texts) != 1 {
		t.Fatalf("woke %d times for one merge", len(texts))
	}
	later := c.Service.Now().Add(reconcile.ReportWindow)
	c.Service.Now = func() time.Time { return later }
	pass(t, c)
	st, _ = c.Service.Store.Snapshot()
	if len(m.status.Windows) != 0 || st.Attempts[at.ID].State != "ended" {
		t.Fatalf("windows %v, attempt %s after the report window", m.status.Windows, st.Attempts[at.ID].State)
	}
	if n := releaseNotices(st, task.ID); len(n) != 1 || !strings.Contains(n[0].Body, "with a background job still running") {
		t.Fatalf("release notices %+v, want one naming the background job", n)
	}
}

// A session busy in a turn is never typed into, and its done task's window
// stays open until it is idle.
func TestASessionBusyInATurnIsLeftAlone(t *testing.T) {
	c, m, task, at := standaloneMergeRequest(t)
	m.status.Windows[0].State = session.StateWorking
	woke := 0
	c.Wake = func(reconcile.Session, string) error { woke++; return nil }
	pass(t, c)
	later := c.Service.Now().Add(reconcile.ReportWindow + reconcile.StallWait)
	c.Service.Now = func() time.Time { return later }
	pass(t, c)
	st, _ := c.Service.Store.Snapshot()
	if st.Items[task.ID].State != "done" || woke != 0 || len(m.status.Windows) != 1 || st.Attempts[at.ID].State == "ended" {
		t.Fatalf("task %s, woke %d, windows %v, attempt %s: want merged, untouched and open",
			st.Items[task.ID].State, woke, m.status.Windows, st.Attempts[at.ID].State)
	}
}

func releaseNotices(st control.State, id string) []control.Update {
	var out []control.Update
	for _, u := range st.Updates {
		if u.Item == id && u.Recipient == "operator" && strings.Contains(u.Body, "was released") {
			out = append(out, u)
		}
	}
	return out
}

// A wait that follows a background spell starts its own clock: the stall
// counts the wait, while release counts the whole idle spell.
func TestAWaitAfterABackgroundSpellStartsItsOwnClock(t *testing.T) {
	start, later := time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC), time.Date(2026, 11, 9, 9, 15, 0, 0, time.UTC)
	bg := reconcile.Session{Live: true, Background: true}
	at := kept(control.Attempt{}, bg, start, true)
	if !at.IdleSince.Equal(start) || !at.WaitingSince.IsZero() {
		t.Fatalf("background: idle %v, waiting %v", at.IdleSince, at.WaitingSince)
	}
	wait := reconcile.Session{Live: true, Waiting: true}
	at = kept(at, wait, later, true)
	if !at.IdleSince.Equal(start) || !at.WaitingSince.Equal(later) {
		t.Fatalf("waiting after background: idle %v, waiting %v; want %v, %v", at.IdleSince, at.WaitingSince, start, later)
	}
	at = kept(at, reconcile.Session{Live: true}, later, true)
	if !at.IdleSince.IsZero() || !at.WaitingSince.IsZero() {
		t.Fatalf("busy in a turn: idle %v, waiting %v; want both cleared", at.IdleSince, at.WaitingSince)
	}
}
