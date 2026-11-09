package controlrun

import (
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/reconcile"
	"github.com/dinoderek/engine/internal/session"
)

func TestEditedCardInvalidatesStalePlacement(t *testing.T) {
	c, h, _, _, task := setup(t)
	pass(t, c)
	before, err := c.Service.Store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	execute(t, c.Service, control.Request{Op: "edit", ID: task.ID, Version: task.Version,
		Item: control.Item{Title: task.Title, Body: task.Body, Requires: []string{"linux"}, Tier: "deep"}})
	if err = c.dispatch(before, reconcile.Action{Item: task.ID, Host: "devbox", Harness: "claude"}, c.Config); err == nil {
		t.Fatal("stale placement reserved an execution")
	}
	after, err := c.Service.Store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Attempts) != 0 || h.calls != 0 {
		t.Fatal("stale placement launched work")
	}
	pass(t, c)
	after, err = c.Service.Store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Attempts) != 0 || h.calls != 0 {
		t.Fatal("recomputed placement ignored new capabilities")
	}
}

func TestPlanQuestionPreventsAutomaticResumeUntilResolved(t *testing.T) {
	c, h, _, plan, task := setup(t)
	pass(t, c)
	pass(t, c)
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	c.Service.Now = func() time.Time { return now }
	h.status.Windows[0].State = session.StateLimited
	h.status.Windows[0].ResetsAt = now.Add(-3 * time.Minute).Unix()
	resumes := 0
	c.Resume = func(reconcile.Session) error { resumes++; return nil }
	question, err := c.Service.Execute(control.Actor{Name: "operator", Role: "operator"},
		control.Request{Op: "ask", ID: plan.ID, Body: "Pause to decide scope", Key: "plan-stop"})
	if err != nil {
		t.Fatal(err)
	}
	pass(t, c)
	st, err := c.Service.Store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if resumes != 0 || st.Attempts[st.Items[task.ID].Attempt].ResumedFor != 0 {
		t.Fatal("unresolved plan question allowed quota recovery")
	}
	_, err = c.Service.Execute(control.Actor{Name: "operator", Role: "operator"},
		control.Request{Op: "resolve", ReplyTo: question.Updates[0].ID, Body: "Continue with the agreed scope", Key: "plan-resolve"})
	if err != nil {
		t.Fatal(err)
	}
	pass(t, c)
	if resumes != 1 {
		t.Fatal("resolved plan question prevented recovery")
	}
}
