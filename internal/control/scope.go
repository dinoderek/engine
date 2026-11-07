package control

func readable(st *State, a Actor, id string) bool {
	if a.Role != "worker" {
		return true
	}
	owner := st.Items[st.Attempts[a.Attempt].Item]
	if id == owner.ID || id == owner.Repo || id == owner.Plan {
		return true
	}
	item := st.Items[id]
	if owner.Kind == "plan" && item.Plan == owner.ID {
		return true
	}
	return dependencyReadable(st, owner, id)
}

func dependencyReadable(st *State, owner Item, id string) bool {
	pending := append([]string{}, owner.Depends...)
	if owner.Kind == "plan" {
		for _, t := range st.Items {
			if t.Plan == owner.ID {
				pending = append(pending, t.Depends...)
			}
		}
	}
	seen := map[string]bool{}
	for len(pending) > 0 {
		next := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if next == id {
			return true
		}
		if seen[next] {
			continue
		}
		seen[next] = true
		pending = append(pending, st.Items[next].Depends...)
	}
	return false
}

func readableUpdate(st *State, a Actor, u Update) bool {
	if a.Role != "worker" {
		return true
	}
	if u.Item != "" {
		return readable(st, a, u.Item)
	}
	for {
		if u.Author == a.Name {
			return true
		}
		if u.ReplyTo < 1 || u.ReplyTo > len(st.Updates) || u.ReplyTo >= u.ID {
			return false
		}
		u = st.Updates[u.ReplyTo-1]
	}
}
