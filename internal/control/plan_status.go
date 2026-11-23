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
// and open question filled in. Neither is ever stored.
func withStatus(tx *Tx, item Item) Item {
	return withStatuses(tx, []Item{item})[0]
}

// withStatuses is withStatus of each of items, reading every plan's tasks and
// open question once for all of them.
func withStatuses(tx *Tx, items []Item) []Item {
	var plans []string
	for _, it := range items {
		if it.Kind == "plan" {
			plans = append(plans, it.ID)
		}
	}
	if len(plans) == 0 {
		return items
	}
	tasks, questions := tx.TaskStatesOfPlans(plans), tx.OwnQuestions(plans)
	ids := make([]int, 0, len(questions))
	for _, q := range questions {
		ids = append(ids, q)
	}
	asked := tx.UpdatesByID(ids)
	out := make([]Item, len(items))
	for i, it := range items {
		out[i] = it
		if it.Kind != "plan" {
			continue
		}
		id := questions[it.ID]
		out[i].Status = PlanStatus(it, tasks[it.ID], id > 0)
		if id > 0 {
			out[i].Question = updateRef(asked[id])
		}
	}
	return out
}
