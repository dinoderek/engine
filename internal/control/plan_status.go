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
	if item.Kind != "plan" {
		return item
	}
	var tasks []Item
	for _, t := range tx.ItemsOfPlan(item.ID) {
		if t.Kind == "task" {
			tasks = append(tasks, t)
		}
	}
	id := OwnQuestion(tx, item.ID)
	item.Status = PlanStatus(item, tasks, id > 0)
	if id > 0 {
		q, _ := tx.Update(id)
		item.Question = updateRef(q)
	}
	return item
}
