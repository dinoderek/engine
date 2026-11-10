package control

import (
	"testing"
	"time"
)

func TestResumableWhenWorkRemainsAfterTheWindowWentAway(t *testing.T) {
	start := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	live := start.Add(time.Hour)
	cases := []struct {
		name       string
		task, plan Item
		at         Attempt
		question   bool
		prev       string
		want       bool
	}{
		{name: "crash while coordinating", at: Attempt{Started: start, LiveAt: live}},
		{name: "has a PR", task: Item{PR: "pr-url"}, at: Attempt{Started: start, LiveAt: live}, want: true},
		{name: "has a PR but was never seen live", task: Item{PR: "pr-url"}, at: Attempt{Started: start}},
		{name: "open question but was never seen live", question: true, at: Attempt{Started: start}},
		{name: "open operator question", question: true, at: Attempt{Started: start, LiveAt: live}, want: true},
		{name: "plan stopped after last seen live", plan: Item{StoppedAt: live.Add(time.Minute)}, at: Attempt{Started: start, LiveAt: live}, want: true},
		{name: "plan stopped as it was last seen live", plan: Item{StoppedAt: live}, at: Attempt{Started: start, LiveAt: live}, want: true},
		{name: "stopped before it was dispatched", plan: Item{StoppedAt: start.Add(-time.Minute)}, at: Attempt{Started: start}},
		{name: "stopped, then seen live after the restart", plan: Item{StoppedAt: live.Add(-time.Minute)}, at: Attempt{Started: start, LiveAt: live}},
		{name: "resume of a vanished attempt", prev: EndVanished, task: Item{PR: "pr-url"}, at: Attempt{Started: start, LiveAt: live}},
		{name: "retry of a failed attempt", prev: EndFailed, task: Item{PR: "pr-url"}, at: Attempt{Started: start, LiveAt: live}, want: true},
		{name: "resume of a vanished attempt, then stopped", prev: EndVanished, plan: Item{StoppedAt: live}, at: Attempt{Started: start, LiveAt: live}, want: true},
		{name: "stopped before it was ever seen live", plan: Item{StoppedAt: start.Add(time.Minute)}, at: Attempt{Started: start}, want: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.plan.ID, c.plan.Kind = "p1", "plan"
			c.task.ID, c.task.Kind, c.task.Plan, c.task.State, c.task.Attempt = "t2", "task", "p1", "running", "a3"
			c.at.ID, c.at.Item, c.at.State = "a3", "t2", "running"
			st := emptyState()
			st.Items[c.plan.ID], st.Items[c.task.ID], st.Attempts[c.at.ID] = c.plan, c.task, c.at
			if c.prev != "" {
				st.Attempts["a1"] = Attempt{ID: "a1", Item: "t2", State: "ended", Reason: c.prev}
				st.Attempts["a9"] = Attempt{ID: "a9", Item: "t8", State: "ended", Reason: EndFailed}
			}
			if c.question {
				st.Updates = append(st.Updates, Update{ID: 1, Item: "t2", Kind: "ask", RequiresReply: true})
			}
			if got := resumable(&st, c.task, c.at); got != c.want {
				t.Fatalf("resumable = %v, want %v", got, c.want)
			}
		})
	}
}

func TestReconcileLeavesAResumableTaskForAResumeAndFailsTheRest(t *testing.T) {
	s := service(t)
	ended := 0
	s.Reconcile = func(string) error { ended++; return nil }
	repo, plan := repoPlan(t, s)
	task := newTask(t, s, repo.ID, plan.ID)
	approve(t, s, plan)
	a, out := dispatch(t, s, task)
	question := call(t, s, a, Request{Op: "ask", ID: task.ID, Body: "Which API?"}).Updates[0]
	seenLive(t, s, out.Attempt.ID)
	at := call(t, s, op, Request{Op: "attempt-reconcile", ID: out.Attempt.ID, Body: "host rebooted"}).Attempt
	current := call(t, s, op, Request{Op: "get", ID: task.ID}).Item
	if at.Reason != EndVanished || at.State != "ended" || current.State != "needs-human" {
		t.Fatalf("a vanished attempt with an open question failed its task: %+v %+v", at, current)
	}
	again := call(t, s, op, Request{Op: "attempt-reconcile", ID: out.Attempt.ID, Body: "again"}).Attempt
	if again.Reason != EndVanished || ended != 2 {
		t.Fatalf("reconciling twice changed the ended attempt: %+v", again)
	}
	reject(t, s, op, Request{Op: "claim", ID: task.ID, Body: "devbox", State: "claude"}, 409)
	call(t, s, op, Request{Op: "resolve", ReplyTo: question.ID, Body: "The second one."})
	_, resumed := dispatch(t, s, *current)
	if resumed.Item.State != "running" || resumed.Attempt.ID == out.Attempt.ID {
		t.Fatalf("an answered, vanished task was not dispatched again: %+v", resumed.Item)
	}
	seenLive(t, s, resumed.Attempt.ID)
	failed := call(t, s, op, Request{Op: "attempt-reconcile", ID: resumed.Attempt.ID, Body: "crashed"}).Attempt
	current = call(t, s, op, Request{Op: "get", ID: task.ID}).Item
	if failed.Reason != EndFailed || current.State != "failed" {
		t.Fatalf("a real failure did not fail its task: %+v %+v", failed, current)
	}
	reject(t, s, op, Request{Op: "claim", ID: task.ID, Body: "devbox", State: "claude"}, 409)
}

func TestStopRecordsWhenCoordinationStopped(t *testing.T) {
	s := service(t)
	_, plan := repoPlan(t, s)
	plan = approve(t, s, plan)
	plan = *call(t, s, op, Request{Op: "stop", ID: plan.ID}).Item
	if !plan.StoppedAt.IsZero() {
		t.Fatal("stopping idle coordination recorded a stop")
	}
	call(t, s, op, Request{Op: "run", ID: plan.ID})
	plan = *call(t, s, op, Request{Op: "stop", ID: plan.ID}).Item
	if !plan.StoppedAt.Equal(s.now()) || plan.Coordinating {
		t.Fatalf("stop did not record its time: %+v", plan)
	}
}

func TestEndDeadIsIdempotentAndRefusesAnUnknownAttempt(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	task := newTask(t, s, repo.ID, plan.ID)
	approve(t, s, plan)
	_, out := dispatch(t, s, task)
	if _, err := s.EndDead("absent", "gone"); err == nil {
		t.Fatal("ended an unknown attempt")
	}
	first, err := s.EndDead(out.Attempt.ID, "gone")
	if err != nil || first.Reason != EndFailed {
		t.Fatalf("%v %+v", err, first)
	}
	second, err := s.EndDead(out.Attempt.ID, "gone")
	if err != nil || !second.Ended.Equal(first.Ended) || second.Reason != EndFailed {
		t.Fatalf("ending twice changed the attempt: %v %+v", err, second)
	}
}

// seenLive records that a coordinator pass saw attempt id's window live.
func seenLive(t *testing.T, s Service, id string) {
	t.Helper()
	if err := s.Store.Transaction(func(st *State) error {
		at := st.Attempts[id]
		at.LiveAt = s.now()
		st.Attempts[id] = at
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
