package control

import (
	"testing"
	"time"
)

func TestClaimedByAWorkerWriteOrALongLife(t *testing.T) {
	start := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		at      Attempt
		updates []Update
		want    bool
	}{
		{name: "never seen live", at: Attempt{ID: "a1", Started: start}},
		{name: "seen live only briefly", at: Attempt{ID: "a1", Started: start, LiveAt: start.Add(ClaimWait - time.Second)}},
		{name: "seen live for the claim wait", at: Attempt{ID: "a1", Started: start, LiveAt: start.Add(ClaimWait)}, want: true},
		{name: "wrote to the server", at: Attempt{ID: "a1", Started: start}, updates: []Update{{ID: 1, Author: "a1"}}, want: true},
		{name: "another author wrote", at: Attempt{ID: "a1", Started: start}, updates: []Update{{ID: 1, Author: "coordinator"}, {ID: 2, Author: "a2"}}},
	}
	for _, c := range cases {
		st := emptyState()
		st.Updates = c.updates
		if got := claimed(&st, c.at); got != c.want {
			t.Errorf("%s: claimed = %v, want %v", c.name, got, c.want)
		}
	}
}

// dies ends attempt id's dead window, as the coordinator does, with a pane
// log tail.
func dies(t *testing.T, s Service, id, tail string) Attempt {
	t.Helper()
	at, err := s.EndDead(id, "its session has died", tail)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func TestAFirstEarlyDeathReturnsTheTaskToTodoAndASecondFailsIt(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	task := newTask(t, s, repo.ID, plan.ID)
	approve(t, s, plan)
	_, first := dispatch(t, s, task)
	launched(t, s, first.Attempt.ID)
	at := dies(t, s, first.Attempt.ID, "boom one")
	current := call(t, s, op, Request{Op: "get", ID: task.ID}).Item
	if at.Reason != EndEarlyDeath || !at.Early || at.Tail != "boom one" || current.State != "todo" {
		t.Fatalf("the first early death: %+v, task %s", at, current.State)
	}
	_, second := dispatch(t, s, *current)
	if second.Attempt.ID == first.Attempt.ID || second.Item.State != "running" {
		t.Fatalf("the task was not dispatched again: %+v", second.Item)
	}
	launched(t, s, second.Attempt.ID)
	at = dies(t, s, second.Attempt.ID, "boom two")
	current = call(t, s, op, Request{Op: "get", ID: task.ID}).Item
	if at.Reason != EndFailed || !at.Early || at.Tail != "boom two" || current.State != "failed" {
		t.Fatalf("the second early death: %+v, task %s", at, current.State)
	}
	reject(t, s, op, Request{Op: "claim", ID: task.ID, Body: "devbox", State: "claude"}, 409)
}

func TestAnEarlyDeathAfterAnUnrelatedVanishCountsAsTheFirst(t *testing.T) {
	s := service(t)
	s.Reconcile = func(string) error { return nil }
	repo, plan := repoPlan(t, s)
	task := newTask(t, s, repo.ID, plan.ID)
	approve(t, s, plan)
	a, first := dispatch(t, s, task)
	launched(t, s, first.Attempt.ID)
	question := call(t, s, a, Request{Op: "ask", ID: task.ID, Body: "Which API?"}).Updates[0]
	seenLive(t, s, first.Attempt.ID)
	if at := dies(t, s, first.Attempt.ID, "ignored"); at.Reason != EndVanished || at.Early || at.Tail != "" {
		t.Fatalf("a claimed session's death was not an ordinary vanish: %+v", at)
	}
	call(t, s, op, Request{Op: "resolve", ReplyTo: question.ID, Body: "The second one."})
	current := call(t, s, op, Request{Op: "get", ID: task.ID}).Item
	_, resumed := dispatch(t, s, *current)
	launched(t, s, resumed.Attempt.ID)
	at := dies(t, s, resumed.Attempt.ID, "")
	current = call(t, s, op, Request{Op: "get", ID: task.ID}).Item
	if at.Reason != EndEarlyDeath || current.State != "todo" {
		t.Fatalf("an early death after a vanish was not the first: %+v, task %s", at, current.State)
	}
}

func TestAnEarlyDeathLeavesAHeldTaskInItsState(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	task := newTask(t, s, repo.ID, plan.ID)
	approve(t, s, plan)
	_, out := dispatch(t, s, task)
	launched(t, s, out.Attempt.ID)
	if err := s.Store.Transaction(func(st *State) error {
		item := st.Items[task.ID]
		item.State = "in-review"
		st.Items[task.ID] = item
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	at := dies(t, s, out.Attempt.ID, "")
	current := call(t, s, op, Request{Op: "get", ID: task.ID}).Item
	if at.Reason != EndEarlyDeath || current.State != "in-review" {
		t.Fatalf("an in-review task changed: %+v, %s", at, current.State)
	}
	st, _ := s.Store.Snapshot()
	if !Vanished(&st, *current) {
		t.Error("an early death did not leave the task ready to dispatch again")
	}
}
