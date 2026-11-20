package controlrun

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/reconcile"
	"github.com/dinoderek/engine/internal/session"
)

// typingOrder records each text typed with what the typed op had logged
// on the host by then, which it then clears.
type typingOrder struct {
	h     *host
	typed []typing
}

type typing struct{ logged, text string }

func (o *typingOrder) record(text string) error {
	logged := ""
	if n := len(o.h.typed); n > 0 {
		logged = o.h.typed[n-1]
	}
	o.typed = append(o.typed, typing{logged, text})
	o.h.typed = nil
	return nil
}

// check fails unless exactly one text was typed, its hash logged in run's
// typed-text log first, and returns it.
func (o *typingOrder) check(t *testing.T, run string) string {
	t.Helper()
	if len(o.typed) != 1 {
		t.Fatalf("typed %+v, want one text", o.typed)
	}
	if got := o.typed[0]; got.logged != run+" "+session.TextHash(got.text) {
		t.Errorf("typed %q with %q logged; want its hash logged in run %s first", got.text, got.logged, run)
	}
	return o.typed[0].text
}

func TestAWakeLogsItsHashBeforeItIsTyped(t *testing.T) {
	c, h, _, _, task := setup(t)
	pass(t, c)
	pass(t, c)
	h.status.Windows[0].State = session.StateWaiting
	order := &typingOrder{h: h}
	c.Wake = func(_ reconcile.Session, text string) error { return order.record(text) }
	execute(t, c.Service, control.Request{Op: "say", ID: task.ID, Body: "Use the second approach"})
	h.typed = nil
	pass(t, c)
	st, _ := c.Service.Store.Snapshot()
	text := order.check(t, st.Attempts[st.Items[task.ID].Attempt].RunID)
	if !strings.HasPrefix(text, "Read the new task and plan updates: engine updates "+task.ID) {
		t.Errorf("typed %q, want the wake", text)
	}
}

func TestAWakeThatCannotBeLoggedIsNotTyped(t *testing.T) {
	c, h, _, _, task := setup(t)
	pass(t, c)
	pass(t, c)
	h.status.Windows[0].State = session.StateWaiting
	woke := 0
	c.Wake = func(reconcile.Session, string) error { woke++; return nil }
	execute(t, c.Service, control.Request{Op: "say", ID: task.ID, Body: "Use the second approach"})
	h.typedErr = errors.New("ssh: connection refused")
	pass(t, c)
	if woke != 0 {
		t.Fatalf("typed a wake whose hash was never logged")
	}
	h.typedErr = nil
	pass(t, c)
	if woke != 1 {
		t.Errorf("woke %d times once the host logged it, want 1", woke)
	}
}

func TestAContinueLogsItsHashBeforeItIsTyped(t *testing.T) {
	c, h, _, _, task := setup(t)
	pass(t, c)
	pass(t, c)
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	c.Service.Now = func() time.Time { return now }
	h.status.Windows[0].State = session.StateLimited
	h.status.Windows[0].ResetsAt = now.Add(-3 * time.Minute).Unix()
	order := &typingOrder{h: h}
	c.Resume = func(reconcile.Session) error { return order.record(ResumeText) }
	h.typed = nil
	pass(t, c)
	st, _ := c.Service.Store.Snapshot()
	order.check(t, st.Attempts[st.Items[task.ID].Attempt].RunID)
}
