package controlrun

import (
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/reconcile"
	"github.com/dinoderek/engine/internal/session"
)

// A session that asks, then waits hours for the answer, is not stalled the
// moment the operator answers: the wait at the hard stop never counts, so
// the stall clock starts at the answer, and its notice gives the real wait.
func TestAWaitAtAnAnsweredQuestionIsNoStall(t *testing.T) {
	c, h, _, _, task := setup(t)
	pass(t, c)
	pass(t, c)
	c.Wake = func(reconcile.Session, string) error { return nil }
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	c.Service.Now = func() time.Time { return now }
	h.status.Windows[0].State = session.StateWaiting
	st, _ := snapshotOf(c.Service.Store)
	attempt := st.Items[task.ID].Attempt
	worker := control.Actor{Name: attempt, Role: "worker", Attempt: attempt}
	out, err := c.Service.Execute(worker, control.Request{Op: "ask", Key: control.Secret(), ID: task.ID, Body: "Which tint?"})
	if err != nil {
		t.Fatal(err)
	}
	// Passes follow each other within OutageGap, or the wait would restart.
	for range 10 {
		pass(t, c)
		now = now.Add(4 * time.Minute)
	}
	execute(t, c.Service, control.Request{Op: "resolve", ReplyTo: out.Updates[0].ID, Body: "Keep the tint"})
	pass(t, c)
	if n := stallNotices(t, c, task.ID); len(n) != 0 {
		t.Fatalf("stalled right after the answer: %q", n)
	}
	for range 3 {
		now = now.Add(4 * time.Minute)
		pass(t, c)
	}
	if n := stallNotices(t, c, task.ID); len(n) != 1 || !strings.Contains(n[0], "has waited for input for 12m without asking through the server") {
		t.Errorf("notices %q, want one stall timed from the answer", n)
	}
}

func stallNotices(t *testing.T, c *Coordinator, id string) []string {
	t.Helper()
	st, err := snapshotOf(c.Service.Store)
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
