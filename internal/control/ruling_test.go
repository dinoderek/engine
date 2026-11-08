package control

import "testing"

func TestPlanScopedResolutionPublishesARulingToTheSiblings(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	one := newTask(t, s, repo.ID, plan.ID)
	two := newTask(t, s, repo.ID, plan.ID)
	approve(t, s, plan)
	first, _ := dispatch(t, s, one)
	second, _ := dispatch(t, s, two)
	question := call(t, s, first, Request{Op: "ask", ID: one.ID, Body: "May the feature change its public API?"}).Updates[0]
	resolve := Request{Op: "resolve", Key: "plan-ruling", ReplyTo: question.ID, Body: "Approved for every task in the plan", Item: Item{Plan: plan.ID}}
	reject(t, s, first, resolve, 403)
	out := call(t, s, op, resolve)
	if len(out.Updates) != 2 {
		t.Fatalf("updates = %+v, want the resolution and the ruling", out.Updates)
	}
	resolution, ruling := out.Updates[0], out.Updates[1]
	if resolution.Kind != "resolved" || resolution.Item != one.ID || resolution.ReplyTo != question.ID {
		t.Fatalf("resolution = %+v", resolution)
	}
	if ruling.Kind != "ruling" || ruling.Item != plan.ID || ruling.ReplyTo != question.ID || ruling.Body != resolution.Body || ruling.Author != "operator" {
		t.Fatalf("ruling = %+v", ruling)
	}
	if retry := call(t, s, op, resolve); len(retry.Updates) != 2 || retry.Updates[1].ID != ruling.ID {
		t.Fatalf("retry = %+v, want the original two updates", retry.Updates)
	}
	seen := call(t, s, second, Request{Op: "updates", ID: plan.ID, After: question.ID}).Updates
	if len(seen) != 1 || seen[0].ID != ruling.ID {
		t.Fatalf("sibling reads %+v, want the ruling", seen)
	}
	if hidden := call(t, s, second, Request{Op: "updates", ID: one.ID}).Updates; len(hidden) != 0 {
		t.Fatalf("sibling reads %+v from another task", hidden)
	}
	st := mustSnapshot(t, s)
	if PendingQuestion(&st, one.ID) != 0 || PendingQuestion(&st, two.ID) != 0 {
		t.Fatal("the ruling left a question pending")
	}
}

func TestPlanScopedResolutionRejectsAnotherPlan(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	other := *call(t, s, op, Request{Op: "create", Item: Item{Kind: "plan", Repo: repo.ID, Title: "Another", Body: "Other"}}).Item
	task := newTask(t, s, repo.ID, plan.ID)
	approve(t, s, plan)
	worker, _ := dispatch(t, s, task)
	question := call(t, s, worker, Request{Op: "ask", ID: task.ID, Body: "Approve?"}).Updates[0]
	before := len(mustSnapshot(t, s).Updates)
	reject(t, s, op, Request{Op: "resolve", ReplyTo: question.ID, Body: "Yes", Item: Item{Plan: other.ID}}, 400)
	reject(t, s, op, Request{Op: "resolve", ReplyTo: question.ID, Body: "Yes", Item: Item{Plan: task.ID}}, 404)
	reject(t, s, op, Request{Op: "resolve", ReplyTo: question.ID, Body: "Yes", Item: Item{Plan: "p999"}}, 404)
	reject(t, s, op, Request{Op: "say", ID: task.ID, Body: "Yes", Item: Item{Plan: plan.ID}}, 400)
	reject(t, s, op, Request{Op: "reply", ReplyTo: question.ID, Body: "Yes", Item: Item{Plan: plan.ID}}, 400)
	if after := len(mustSnapshot(t, s).Updates); after != before {
		t.Fatalf("rejected rulings wrote %d updates", after-before)
	}
	planQuestion := call(t, s, op, Request{Op: "ask", ID: plan.ID, Body: "Scope?"}).Updates[0]
	if out := call(t, s, op, Request{Op: "resolve", ReplyTo: planQuestion.ID, Body: "Narrow", Item: Item{Plan: plan.ID}}); len(out.Updates) != 2 || out.Updates[1].Item != plan.ID {
		t.Fatalf("plan question ruling = %+v", out.Updates)
	}
	if out := call(t, s, op, Request{Op: "resolve", ReplyTo: question.ID, Body: "Only this task"}); len(out.Updates) != 1 {
		t.Fatalf("task-only resolution = %+v, want no ruling", out.Updates)
	}
}
