package control

import "testing"

// running is an approved task whose execution has launched, with its
// worker's credential.
func running(t *testing.T, s Service) (Item, Actor, Attempt) {
	t.Helper()
	repo, plan := repoPlan(t, s)
	task := newTask(t, s, repo.ID, plan.ID)
	approve(t, s, plan)
	worker, out := dispatch(t, s, task)
	launched(t, s, out.Attempt.ID)
	return task, worker, *out.Attempt
}

func TestStopHoldsTheTaskAndEndsItsAttemptInOneTransaction(t *testing.T) {
	s := service(t)
	task, worker, at := running(t, s)
	out := call(t, s, op, Request{Op: "task-stop", ID: task.ID})
	if out.Item.State != "todo" || !out.Item.Held || out.Item.Version != 3 || out.Attempt.Reason != EndStopped || out.Attempt.State != "ended" {
		t.Fatalf("stop: %+v, attempt %+v", out.Item, out.Attempt)
	}
	st, err := s.Store.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Attempts[at.ID]; got.State != "ended" || got.Reason != EndStopped || got.Ended.IsZero() {
		t.Fatalf("stored attempt %+v", got)
	}
	if Vanished(st.tx(), st.Items[task.ID]) {
		t.Error("a stopped attempt is not a vanished one: the task would be dispatched again as a resume")
	}
	// The stopped session cannot write, and nothing dispatches the held task.
	reject(t, s, worker, Request{Op: "say", ID: task.ID, Body: "still here"}, 403)
	reject(t, s, Actor{Name: "coordinator", Role: "coordinator"}, Request{Op: "claim", ID: task.ID, Version: out.Item.Version, Body: "devbox", State: "claude"}, 409)
	reject(t, s, op, Request{Op: "claim", ID: task.ID, Body: "devbox", State: "claude"}, 409)
}

func TestStopWithdrawsTheTasksMergeQueueEntry(t *testing.T) {
	s := service(t)
	task, worker, _ := running(t, s)
	call(t, s, worker, Request{Op: "state", ID: task.ID, Version: 2, State: "in-review", Item: Item{PR: good().PR}})
	call(t, s, worker, ready(task.ID, good()))
	if q := call(t, s, op, Request{Op: "merge-queue", ID: task.Repo}).Queue; len(q) != 1 {
		t.Fatalf("queue before the stop: %+v", q)
	}
	out := call(t, s, op, Request{Op: "task-stop", ID: task.ID})
	if out.Item.State != "todo" || !out.Item.Held {
		t.Fatalf("stop: %+v", out.Item)
	}
	if q := call(t, s, op, Request{Op: "merge-queue", ID: task.Repo}).Queue; len(q) != 0 {
		t.Fatalf("queue after the stop: %+v", q)
	}
}

func TestAHeldTaskCanBeEditedAndResumedToBeDispatchedAgain(t *testing.T) {
	s := service(t)
	task, _, first := running(t, s)
	held := call(t, s, op, Request{Op: "task-stop", ID: task.ID}).Item
	edited := call(t, s, op, Request{Op: "edit", ID: task.ID, Version: held.Version, Item: Item{Title: "Build it differently", Body: "## Outcome\nWorks\n"}}).Item
	if !edited.Held || edited.State != "todo" || edited.Title != "Build it differently" {
		t.Fatalf("a card edit changed the hold: %+v", edited)
	}
	reject(t, s, op, Request{Op: "task-resume", ID: task.ID, Version: held.Version}, 409)
	resumed := call(t, s, op, Request{Op: "task-resume", ID: task.ID, Version: edited.Version}).Item
	if resumed.Held || resumed.State != "todo" || resumed.Version != edited.Version+1 {
		t.Fatalf("resume: %+v", resumed)
	}
	_, again := dispatch(t, s, *resumed)
	if again.Attempt.ID == first.ID || again.Item.State != "running" {
		t.Fatalf("the resumed task was not dispatched anew: %+v", again)
	}
}

func TestStopAndResumeRefuseWhatTheyCannotDo(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	idle := newTask(t, s, repo.ID, plan.ID)
	approve(t, s, plan)
	reject(t, s, op, Request{Op: "task-stop", ID: idle.ID}, 409)
	reject(t, s, op, Request{Op: "task-resume", ID: idle.ID}, 409)
	reject(t, s, op, Request{Op: "task-stop", ID: plan.ID}, 404)
	reject(t, s, op, Request{Op: "task-stop", ID: "t999"}, 404)
	worker, out := dispatch(t, s, idle)
	launched(t, s, out.Attempt.ID)
	for _, a := range []Actor{worker, {Name: "coordinator", Role: "coordinator"}} {
		reject(t, s, a, Request{Op: "task-stop", ID: idle.ID}, 403)
		reject(t, s, a, Request{Op: "task-resume", ID: idle.ID}, 403)
	}
	reject(t, s, op, Request{Op: "task-stop", ID: idle.ID, Version: 1}, 409)
	call(t, s, op, Request{Op: "task-stop", ID: idle.ID, Version: out.Item.Version})
	reject(t, s, op, Request{Op: "task-stop", ID: idle.ID}, 409)
	reject(t, s, op, Request{Op: "create", Item: Item{Kind: "task", Repo: repo.ID, Plan: plan.ID, Title: "Held", Body: "x", Held: true}}, 400)
}

func TestStopOfADispatchInFlightIsRefused(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	task := newTask(t, s, repo.ID, plan.ID)
	approve(t, s, plan)
	dispatch(t, s, task)
	reject(t, s, op, Request{Op: "task-stop", ID: task.ID}, 409)
}

func TestStopOfAFinishedTaskIsRefused(t *testing.T) {
	s := service(t)
	task, worker, _ := running(t, s)
	call(t, s, worker, Request{Op: "state", ID: task.ID, Version: 2, State: "done"})
	reject(t, s, op, Request{Op: "task-stop", ID: task.ID}, 409)
}
