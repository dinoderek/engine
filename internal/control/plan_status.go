package control

import "slices"

// PlanStatus is a plan's state as people read it. Only prepare, approved,
// planned and done are stored. An approved or planned plan is blocked while
// the operator is needed: asked (an unresolved question on the plan), or a
// task at a hard stop or failed. A planned plan is executing once any task
// has left todo.
func PlanStatus(plan Item, tasks []Item, asked bool) string {
	if plan.State != "approved" && plan.State != "planned" {
		return plan.State
	}
	if asked || slices.ContainsFunc(tasks, func(t Item) bool { return t.State == "needs-human" || t.State == "failed" }) {
		return "blocked"
	}
	if plan.State == "planned" && slices.ContainsFunc(tasks, func(t Item) bool { return t.State != "todo" }) {
		return "executing"
	}
	return plan.State
}

// withStatus returns a read response's copy of item, with a plan's status
// filled in. The status is never stored.
func withStatus(st *State, item Item) Item {
	if item.Kind != "plan" {
		return item
	}
	var tasks []Item
	for _, t := range st.Items {
		if t.Kind == "task" && t.Plan == item.ID {
			tasks = append(tasks, t)
		}
	}
	item.Status = PlanStatus(item, tasks, PendingQuestion(st, item.ID) > 0)
	return item
}
