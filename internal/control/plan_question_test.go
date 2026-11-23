package control

import (
	"testing"
	"time"
)

func TestPlanQuestionBlocksOnlyNewDispatches(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	task := newTask(t, s, repo.ID, plan.ID)
	plan = approve(t, s, plan)
	planner, planned := dispatch(t, s, plan)
	child, claimed := dispatch(t, s, task)
	question := call(t, s, planner, Request{Op: "ask", ID: plan.ID, Body: "May the feature change its public API?"}).Updates[0]
	reject(t, s, planner, Request{Op: "state", ID: plan.ID, Version: planned.Item.Version, State: "planned"}, 409)

	// The running task finishes and gets its merge through the question.
	call(t, s, child, Request{Op: "state", ID: task.ID, Version: claimed.Item.Version, State: "in-review", Item: Item{PR: good().PR}})
	call(t, s, child, ready(task.ID, good()))
	st := mustSnapshot(t, s)
	if _, queued := st.Queue[task.ID]; !queued || OwnQuestion(st.tx(), task.ID) != 0 || PlanQuestion(st.tx(), task.ID) != question.ID {
		t.Fatalf("queued %v, own %d, plan %d; want a queued task held by no own question", queued, OwnQuestion(st.tx(), task.ID), PlanQuestion(st.tx(), task.ID))
	}

	// A task that never started is held; one that did is dispatched again.
	other := newTask(t, s, repo.ID, plan.ID)
	reject(t, s, op, Request{Op: "claim", ID: other.ID, Body: "devbox", State: "claude"}, 409)
	if err := s.Store.modify(func(st *state) error {
		at := st.Attempts[claimed.Attempt.ID]
		at.LiveAt = time.Date(2026, 11, 7, 8, 0, 0, 0, time.UTC)
		st.Attempts[at.ID] = at
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if at, err := s.EndDead(claimed.Attempt.ID, "window gone", ""); err != nil || at.Reason != EndVanished {
		t.Fatal(at, err)
	}
	if _, resumed := dispatch(t, s, task); resumed.Item.State != "in-review" {
		t.Fatalf("recovered task is %s, want it back in-review", resumed.Item.State)
	}
	call(t, s, op, Request{Op: "ack", ReplyTo: question.ID})
	call(t, s, op, Request{Op: "reply", ReplyTo: question.ID, Body: "Investigating"})
	reject(t, s, op, Request{Op: "claim", ID: other.ID, Body: "devbox", State: "claude"}, 409)
	call(t, s, op, Request{Op: "resolve", ReplyTo: question.ID, Body: "Approved for the plan revision in the question"})
	call(t, s, planner, Request{Op: "state", ID: plan.ID, Version: planned.Item.Version, State: "planned"})
	call(t, s, op, Request{Op: "claim", ID: other.ID, Body: "devbox", State: "claude"})
}

func TestPlanQuestionLeavesAnotherTasksStateAndMergeAlone(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	one := newTask(t, s, repo.ID, plan.ID)
	two := newTask(t, s, repo.ID, plan.ID)
	approve(t, s, plan)
	first, firstClaim := dispatch(t, s, one)
	second, secondClaim := dispatch(t, s, two)
	call(t, s, first, Request{Op: "ask", ID: plan.ID, Body: "The unit lane is red on main"})
	// The asker is held through its own record; the sibling is not.
	reject(t, s, op, Request{Op: "state", ID: one.ID, Version: firstClaim.Item.Version + 1, State: "in-review", Item: Item{PR: good().PR}}, 409)
	review := call(t, s, second, Request{Op: "state", ID: two.ID, Version: secondClaim.Item.Version, State: "in-review", Item: Item{PR: good().PR}}).Item
	call(t, s, second, ready(two.ID, good()))
	st := mustSnapshot(t, s)
	if _, queued := st.Queue[two.ID]; !queued {
		t.Fatal("a sibling's merge request was refused under a plan question")
	}
	call(t, s, op, Request{Op: "state", ID: two.ID, Version: review.Version, State: "done"})
}

func mustSnapshot(t *testing.T, s Service) state {
	t.Helper()
	st, err := s.Store.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	return st
}
