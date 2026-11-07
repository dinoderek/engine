package control

import (
	"reflect"
	"slices"
	"testing"
)

func TestPlanViewIncludesTransitiveExternalDependencies(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	upstream := newTask(t, s, repo.ID, "")
	middle := *call(t, s, op, Request{Op: "create", Item: Item{Kind: "task", Repo: repo.ID, Title: "Middle", Depends: []string{upstream.ID}}}).Item
	child := *call(t, s, op, Request{Op: "create", Item: Item{Kind: "task", Repo: repo.ID, Plan: plan.ID, Title: "Child", Depends: []string{middle.ID}}}).Item
	other := newTask(t, s, repo.ID, "")
	before := mustSnapshot(t, s)
	view := call(t, s, op, Request{Op: "plan-view", ID: plan.ID})
	var ids []string
	for _, item := range view.Items {
		ids = append(ids, item.ID)
	}
	for _, id := range []string{repo.ID, plan.ID, child.ID, middle.ID, upstream.ID} {
		if !slices.Contains(ids, id) {
			t.Fatal("view omitted", id)
		}
	}
	if len(ids) != 5 || slices.Contains(ids, other.ID) || !reflect.DeepEqual(before, mustSnapshot(t, s)) {
		t.Fatal("view included unrelated items or changed state", ids)
	}
	reject(t, s, op, Request{Op: "plan-view", ID: child.ID}, 404)
	reject(t, s, op, Request{Op: "plan-view", ID: "p999"}, 404)
}

func TestPlanViewWorkerScope(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	own := newTask(t, s, repo.ID, plan.ID)
	otherPlan := *call(t, s, op, Request{Op: "create", Item: Item{Kind: "plan", Repo: repo.ID, Title: "Other"}}).Item
	approve(t, s, plan)
	planner, _ := dispatch(t, s, plan)
	call(t, s, planner, Request{Op: "plan-view", ID: plan.ID})
	reject(t, s, planner, Request{Op: "plan-view", ID: otherPlan.ID}, 403)
	worker, _ := dispatch(t, s, own)
	reject(t, s, worker, Request{Op: "plan-view", ID: plan.ID}, 403)
}
