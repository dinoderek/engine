package controlrun

import (
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/reconcile"
	"github.com/dinoderek/engine/internal/session"
)

func TestDeathDuringUnresolvedQuestionParksTheTaskUntilAnswered(t *testing.T) {
	c, h, _, _, task := setup(t)
	pass(t, c)
	pass(t, c)
	pass(t, c) // the coordinator sees the session live
	execute(t, c.Service, control.Request{Op: "ask", ID: task.ID, Body: "Need an operator decision"})
	st, _ := c.Service.Store.Snapshot()
	first := st.Items[task.ID].Attempt
	h.status.Windows[0].Alive = false
	pass(t, c)
	pass(t, c)
	st, _ = c.Service.Store.Snapshot()
	if at := st.Attempts[first]; at.State != "ended" || at.Reason != control.EndVanished || st.Items[task.ID].State != "needs-human" {
		t.Fatalf("a session that died during a question failed its task: %+v %s", at, st.Items[task.ID].State)
	}
	if h.calls != 1 || st.Items[task.ID].Attempt != first {
		t.Fatal("a task parked on an operator question was dispatched again")
	}
	execute(t, c.Service, control.Request{Op: "resolve", ReplyTo: control.PendingQuestion(&st, task.ID), Body: "Take the first option."})
	pass(t, c)
	st, _ = c.Service.Store.Snapshot()
	resumed := st.Attempts[st.Items[task.ID].Attempt]
	if resumed.ID == first || st.Items[task.ID].State != "running" || !h.launches[resumed.RunID].Spawn.Resume {
		t.Fatalf("the answered task was not dispatched again as a resume: %+v", resumed)
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
	c.Wake = func(reconcile.Session, string) error { wakes++; return nil }
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
	c.Resume = func(reconcile.Session) error { resumed++; return nil }
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

func TestRebootWhileThePlanIsStoppedLeavesTheTaskResumable(t *testing.T) {
	c, h, _, plan, task := setup(t)
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	c.Service.Now = func() time.Time { return now }
	pass(t, c)
	pass(t, c)
	st, _ := c.Service.Store.Snapshot()
	first := st.Attempts[st.Items[task.ID].Attempt]
	now = now.Add(time.Minute)
	execute(t, c.Service, control.Request{Op: "stop", ID: plan.ID})
	h.status.Windows = nil // the host rebooted: its tmux server, and the window, are gone
	now = now.Add(time.Hour)
	execute(t, c.Service, control.Request{Op: "run", ID: plan.ID})
	pass(t, c)
	st, _ = c.Service.Store.Snapshot()
	if at := st.Attempts[first.ID]; at.Reason != control.EndVanished || st.Items[task.ID].State != "running" {
		t.Fatalf("a window lost while the plan was stopped failed its task: %+v %s", at, st.Items[task.ID].State)
	}
	pass(t, c)
	st, _ = c.Service.Store.Snapshot()
	resumed := st.Attempts[st.Items[task.ID].Attempt]
	if resumed.ID == first.ID || !h.launches[resumed.RunID].Spawn.Resume {
		t.Fatalf("the resumable task was not dispatched again as a resume: %+v", resumed)
	}
}

func TestDeathAfterCoordinationRestartedStillFails(t *testing.T) {
	c, h, _, plan, task := setup(t)
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	c.Service.Now = func() time.Time { return now }
	pass(t, c)
	execute(t, c.Service, control.Request{Op: "stop", ID: plan.ID})
	now = now.Add(time.Minute)
	execute(t, c.Service, control.Request{Op: "run", ID: plan.ID})
	pass(t, c)
	h.status.Windows[0].Alive = false
	now = now.Add(time.Minute)
	pass(t, c)
	st, _ := c.Service.Store.Snapshot()
	at := st.Attempts[st.Items[task.ID].Attempt]
	if at.Reason != control.EndFailed || st.Items[task.ID].State != "failed" || h.calls != 1 {
		t.Fatalf("a session that died after coordination restarted did not fail: %+v %s", at, st.Items[task.ID].State)
	}
}

func TestAPlanQuestionParksAVanishedTaskUntilAnswered(t *testing.T) {
	c, h, _, plan, task := setup(t)
	pass(t, c)
	pass(t, c)
	pass(t, c) // the coordinator sees the session live
	execute(t, c.Service, control.Request{Op: "ask", ID: plan.ID, Body: "Is the plan still right?"})
	h.status.Windows = nil
	pass(t, c)
	pass(t, c)
	st, _ := c.Service.Store.Snapshot()
	first := st.Attempts[st.Items[task.ID].Attempt]
	if first.Reason != control.EndVanished || h.calls != 1 {
		t.Fatalf("a task under an open plan question was failed or dispatched again: %+v, %d launches", first, h.calls)
	}
	execute(t, c.Service, control.Request{Op: "resolve", ReplyTo: control.PendingQuestion(&st, task.ID), Body: "Yes."})
	pass(t, c)
	st, _ = c.Service.Store.Snapshot()
	if resumed := st.Attempts[st.Items[task.ID].Attempt]; resumed.ID == first.ID || !h.launches[resumed.RunID].Spawn.Resume {
		t.Fatalf("the answered task was not resumed: %+v", resumed)
	}
}

func TestRebootWhileAStandaloneTaskIsStoppedLeavesItResumable(t *testing.T) {
	c, h, repo, plan, _ := setup(t)
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	c.Service.Now = func() time.Time { return now }
	execute(t, c.Service, control.Request{Op: "stop", ID: plan.ID})
	task := execute(t, c.Service, control.Request{Op: "create", Item: control.Item{Kind: "task", Title: "Alone", Repo: repo.ID, Body: "## Outcome\nWorks\n## Tests\nProve it"}})
	task = execute(t, c.Service, control.Request{Op: "approve", ID: task.ID, Version: task.Version})
	execute(t, c.Service, control.Request{Op: "run", ID: task.ID, Version: task.Version})
	pass(t, c)
	pass(t, c)
	now = now.Add(time.Minute)
	execute(t, c.Service, control.Request{Op: "stop", ID: task.ID})
	h.status.Windows = nil
	now = now.Add(time.Hour)
	execute(t, c.Service, control.Request{Op: "run", ID: task.ID})
	pass(t, c)
	pass(t, c)
	st, _ := c.Service.Store.Snapshot()
	resumed := st.Attempts[st.Items[task.ID].Attempt]
	if st.Items[task.ID].State != "running" || h.calls != 2 || !h.launches[resumed.RunID].Spawn.Resume {
		t.Fatalf("a standalone task stopped across a reboot was not resumed: %s %+v", st.Items[task.ID].State, resumed)
	}
}

func TestAPassRecordsASightingOnlyWhileCoordinating(t *testing.T) {
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	if at := kept(control.Attempt{}, reconcile.Session{Live: true}, now, false); !at.LiveAt.IsZero() {
		t.Error("a pass overlapping a stop recorded a sighting")
	}
	if at := kept(control.Attempt{}, reconcile.Session{Live: true}, now, true); !at.LiveAt.Equal(now) {
		t.Error("a live window was not recorded")
	}
	if at := kept(control.Attempt{LiveAt: now}, reconcile.Session{}, now.Add(time.Minute), true); !at.LiveAt.Equal(now) {
		t.Error("a dead window moved the last sighting")
	}
}
