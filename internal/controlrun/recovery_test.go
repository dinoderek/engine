package controlrun

import (
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/run"
	"github.com/dinoderek/engine/internal/session"
)

func TestDeathDuringUnresolvedQuestionFailsIdempotently(t *testing.T) {
	c, h, _, _, task := setup(t)
	pass(t, c)
	pass(t, c)
	execute(t, c.Service, control.Request{Op: "ask", ID: task.ID, Body: "Need an operator decision"})
	h.status.Windows[0].Alive = false
	pass(t, c)
	pass(t, c)
	st, _ := c.Service.Store.Snapshot()
	if st.Items[task.ID].State != "failed" || st.Attempts[st.Items[task.ID].Attempt].State != "ended" {
		t.Fatal("failed session left in needs-human")
	}
}

func TestPlaceholderRunCannotEndOrCompleteDispatch(t *testing.T) {
	c, h, _, _, task := setup(t)
	pass(t, c)
	out, err := c.Service.Dispatch(task, "devbox", "claude", "reserved")
	if err != nil {
		t.Fatal(err)
	}
	h.status.Runs = []hostops.Run{{Record: session.Record{RunID: out.Attempt.RunID, Tmux: "engine:"}}}
	if err = c.ConfirmEnded(out.Attempt.ID); err == nil {
		t.Fatal("incomplete dispatch treated as ended")
	}
	pass(t, c)
	st, _ := c.Service.Store.Snapshot()
	if st.Attempts[out.Attempt.ID].Window == "" || st.Items[task.ID].State != "running" {
		t.Fatal("placeholder was adopted")
	}
}

func TestWaitingMessagesAndCompletedSessionCleanup(t *testing.T) {
	c, h, _, plan, task := setup(t)
	pass(t, c)
	pass(t, c)
	h.status.Windows[0].State = session.StateWaiting
	wakes := 0
	c.Wake = func(run.Session, string) error { wakes++; return nil }
	execute(t, c.Service, control.Request{Op: "say", ID: task.ID, Body: "Use the second approach"})
	pass(t, c)
	pass(t, c)
	if wakes != 1 {
		t.Fatalf("woke %d times", wakes)
	}
	st, _ := c.Service.Store.Snapshot()
	execute(t, c.Service, control.Request{Op: "state", ID: task.ID, Version: st.Items[task.ID].Version, State: "done"})
	pass(t, c)
	pass(t, c)
	st, _ = c.Service.Store.Snapshot()
	if st.Items[plan.ID].Coordinating || st.Attempts[st.Items[task.ID].Attempt].State != "ended" || len(h.status.Windows) != 0 {
		t.Fatal("completed sessions not cleaned up")
	}
}

func TestQuotaMarkersAreIndependentForEachReset(t *testing.T) {
	c, h, _, _, task := setup(t)
	pass(t, c)
	pass(t, c)
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	c.Service.Now = func() time.Time { return now }
	h.status.Windows[0].State = session.StateLimited
	h.status.Windows[0].ResetsAt = now.Add(-3 * time.Minute).Unix()
	resumed := 0
	c.Resume = func(run.Session) error { resumed++; return nil }
	pass(t, c)
	pass(t, c)
	if resumed != 1 {
		t.Fatalf("resumed %d times", resumed)
	}
	now = now.Add(11 * time.Minute)
	pass(t, c)
	st, _ := c.Service.Store.Snapshot()
	at := st.Attempts[st.Items[task.ID].Attempt]
	if at.StuckFor != h.status.Windows[0].ResetsAt {
		t.Fatal("stuck alert suppressed")
	}
	h.status.Windows[0].ResetsAt = now.Add(-3 * time.Minute).Unix()
	pass(t, c)
	if resumed != 2 {
		t.Fatal("later reset did not allow resume")
	}
}

func TestRetriedTaskDispatchesAsAResume(t *testing.T) {
	c, h, _, _, task := setup(t)
	pass(t, c)
	pass(t, c)
	st, _ := c.Service.Store.Snapshot()
	first := st.Attempts[st.Items[task.ID].Attempt]
	if h.launches[first.RunID].Spawn.Resume {
		t.Fatal("a first attempt dispatched as a resume")
	}
	h.status.Windows[0].Alive = false
	if err := c.ConfirmEnded(first.ID); err != nil {
		t.Fatal(err)
	}
	pass(t, c)
	st, _ = c.Service.Store.Snapshot()
	execute(t, c.Service, control.Request{Op: "state", ID: task.ID, Version: st.Items[task.ID].Version, State: "todo"})
	pass(t, c)
	pass(t, c)
	st, _ = c.Service.Store.Snapshot()
	retry := st.Attempts[st.Items[task.ID].Attempt]
	if retry.ID == first.ID {
		t.Fatal("the retried task was not dispatched again")
	}
	if !h.launches[retry.RunID].Spawn.Resume {
		t.Error("a retry did not dispatch as a resume, so its branch on origin would be refused")
	}
}

func TestOnlyAnEarlierAttemptThatRanMakesAResume(t *testing.T) {
	st := control.State{Attempts: map[string]control.Attempt{
		"a1": {ID: "a1", Item: "t1", State: "ended"},
		"a2": {ID: "a2", Item: "t1", State: "dispatching"},
		"a3": {ID: "a3", Item: "t2", State: "ended", Window: "@3"},
	}}
	if hadEarlierAttempt(st, "t1", "a2") {
		t.Error("an attempt that never opened a window, such as a refused one, made a resume")
	}
	st.Attempts["a1"] = control.Attempt{ID: "a1", Item: "t1", State: "ended", Window: "@1"}
	if !hadEarlierAttempt(st, "t1", "a2") || hadEarlierAttempt(st, "t1", "a1") {
		t.Error("only another attempt of the same task that ran makes a resume")
	}
}
