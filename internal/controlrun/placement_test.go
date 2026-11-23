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
	// The pass decided on the records as they were before the edit.
	inTx(t, c.Service.Store, func(tx *control.Tx) {
		execute(t, c.Service, control.Request{Op: "edit", ID: task.ID, Version: task.Version,
			Item: control.Item{Title: task.Title, Body: task.Body, Requires: []string{"linux"}, Tier: "deep"}})
		if err := c.dispatch(tx, reconcile.Action{Item: task.ID, Host: "devbox", Harness: "claude"}, c.Config); err == nil {
			t.Fatal("stale placement reserved an execution")
		}
	})
	after, err := snapshotOf(c.Service.Store)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Attempts) != 0 || h.calls != 0 {
		t.Fatal("stale placement launched work")
	}
	pass(t, c)
	after, err = snapshotOf(c.Service.Store)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Attempts) != 0 || h.calls != 0 {
		t.Fatal("recomputed placement ignored new capabilities")
	}
}

// limitedAskedOn starts a task whose session hit a limit that has reset, then
// asks a question on the item, and runs a pass.
func limitedAskedOn(t *testing.T, onPlan bool) (c *Coordinator, task control.Item, question control.Response, resumes *int) {
	c, h, _, plan, task := setup(t)
	pass(t, c)
	pass(t, c)
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	c.Service.Now = func() time.Time { return now }
	h.status.Windows[0].State = session.StateLimited
	h.status.Windows[0].ResetsAt = now.Add(-3 * time.Minute).Unix()
	resumes = new(int)
	c.Resume = func(reconcile.Session, func(string) error) error { *resumes++; return nil }
	target := task.ID
	if onPlan {
		target = plan.ID
	}
	question, err := c.Service.Execute(control.Actor{Name: "operator", Role: "operator"},
		control.Request{Op: "ask", ID: target, Body: "Pause to decide scope", Key: "stop"})
	if err != nil {
		t.Fatal(err)
	}
	pass(t, c)
	return c, task, question, resumes
}

func TestATaskQuestionPreventsAutomaticResumeUntilResolved(t *testing.T) {
	c, task, question, resumes := limitedAskedOn(t, false)
	st, err := snapshotOf(c.Service.Store)
	if err != nil {
		t.Fatal(err)
	}
	if *resumes != 0 || st.Attempts[st.Items[task.ID].Attempt].ResumedFor != 0 {
		t.Fatal("unresolved task question allowed quota recovery")
	}
	_, err = c.Service.Execute(control.Actor{Name: "operator", Role: "operator"},
		control.Request{Op: "resolve", ReplyTo: question.Updates[0].ID, Body: "Continue with the agreed scope", Key: "resolve"})
	if err != nil {
		t.Fatal(err)
	}
	pass(t, c)
	if *resumes != 1 {
		t.Fatal("resolved task question prevented recovery")
	}
}

func TestAPlanQuestionDoesNotPreventAutomaticResume(t *testing.T) {
	_, _, _, resumes := limitedAskedOn(t, true)
	if *resumes != 1 {
		t.Fatal("a plan question held back a running task's quota recovery")
	}
}

func TestAPlanQuestionKeepsANewTaskOutOfPlacement(t *testing.T) {
	c, h, _, plan, task := setup(t)
	execute(t, c.Service, control.Request{Op: "ask", ID: plan.ID, Body: "Pause to decide scope"})
	pass(t, c)
	st, err := snapshotOf(c.Service.Store)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Attempts) != 0 || h.calls != 0 || st.Items[task.ID].State != "todo" {
		t.Fatalf("a held task was placed: %d attempts, %d launches, %s", len(st.Attempts), h.calls, st.Items[task.ID].State)
	}
}
