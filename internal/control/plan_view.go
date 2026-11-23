package control

// planView returns a consistent plan snapshot using the existing read scope.
// A task credential cannot lint a partial view of its parent's task graph.
func planView(tx *Tx, a Actor, id string) (Response, error) {
	item, ok := tx.Item(id)
	if !ok || item.Kind != "plan" {
		return Response{}, problem(404, "unknown plan")
	}
	if at, _ := tx.Attempt(a.Attempt); a.Role == "worker" && at.Item != id {
		return Response{}, problem(403, "only the plan execution can read its full plan view")
	}
	repo, _ := tx.Item(item.Repo)
	out := Response{Items: []Item{withStatus(tx, item), repo}}
	seen := map[string]bool{}
	var pending []string
	own := map[string]Item{}
	for _, t := range tx.ItemsOfPlan(id) {
		pending = append(pending, t.ID)
		own[t.ID] = t
	}
	for len(pending) > 0 {
		next := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if seen[next] {
			continue
		}
		seen[next] = true
		if !readable(tx, a, next) {
			return Response{}, problem(403, "dependency is outside this execution's read scope")
		}
		task, ok := own[next]
		if !ok {
			task, _ = tx.Item(next)
		}
		out.Items = append(out.Items, task)
		pending = append(pending, task.Depends...)
	}
	return out, nil
}
