package control

import "testing"

func TestPlanQuestionBlocksHandoffDispatchAndChildMerge(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	task := newTask(t, s, repo.ID, plan.ID)
	plan = approve(t, s, plan)
	planner, planned := dispatch(t, s, plan)
	child, claimed := dispatch(t, s, task)
	review := call(t, s, child, Request{Op: "state", ID: task.ID, Version: claimed.Item.Version, State: "in-review", Item: Item{PR: good().PR}}).Item
	question := call(t, s, planner, Request{Op: "ask", ID: plan.ID, Body: "May the feature change its public API?"}).Updates[0]
	reject(t, s, planner, Request{Op: "state", ID: plan.ID, Version: planned.Item.Version, State: "planned"}, 409)
	reject(t, s, child, ready(task.ID, good()), 409)
	reject(t, s, child, Request{Op: "state", ID: task.ID, Version: review.Version, State: "done"}, 409)
	other := newTask(t, s, repo.ID, plan.ID)
	reject(t, s, op, Request{Op: "claim", ID: other.ID, Body: "devbox", State: "claude"}, 409)
	call(t, s, op, Request{Op: "ack", ReplyTo: question.ID})
	call(t, s, op, Request{Op: "reply", ReplyTo: question.ID, Body: "Investigating"})
	st := mustSnapshot(t, s)
	if PendingQuestion(&st, task.ID) == 0 {
		t.Fatal("ordinary response cleared the plan hard stop")
	}
	call(t, s, op, Request{Op: "resolve", ReplyTo: question.ID, Body: "Approved for the plan revision in the question"})
	call(t, s, planner, Request{Op: "state", ID: plan.ID, Version: planned.Item.Version, State: "planned"})
	call(t, s, child, ready(task.ID, good()))
	call(t, s, op, Request{Op: "claim", ID: other.ID, Body: "devbox", State: "claude"})
}

func mustSnapshot(t *testing.T, s Service) State {
	t.Helper()
	st, err := s.Store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	return st
}
