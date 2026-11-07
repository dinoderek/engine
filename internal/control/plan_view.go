package control

// planView returns a consistent plan snapshot using the existing read scope.
// A task credential cannot lint a partial view of its parent's task graph.
func planView(st *State, a Actor, id string) (Response, error) {
	item, ok := st.Items[id]
	if !ok || item.Kind != "plan" {
		return Response{}, problem(404, "unknown plan")
	}
	if a.Role == "worker" && st.Attempts[a.Attempt].Item != id {
		return Response{}, problem(403, "only the plan execution can read its full plan view")
	}
	out := Response{Items: []Item{item, st.Items[item.Repo]}}
	seen := map[string]bool{}
	var pending []string
	for _, t := range st.Items {
		if t.Plan == id {
			pending = append(pending, t.ID)
		}
	}
	for len(pending) > 0 {
		next := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if seen[next] {
			continue
		}
		seen[next] = true
		if !readable(st, a, next) {
			return Response{}, problem(403, "dependency is outside this execution's read scope")
		}
		task := st.Items[next]
		out.Items = append(out.Items, task)
		pending = append(pending, task.Depends...)
	}
	return out, nil
}
