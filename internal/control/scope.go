package control

// executionOwner is the item a worker's execution runs: its task, or its
// plan for a planner.
func executionOwner(tx *Tx, a Actor) Item {
	at, _ := tx.Attempt(a.Attempt)
	owner, _ := tx.Item(at.Item)
	return owner
}

func readable(tx *Tx, a Actor, id string) bool {
	if a.Role != "worker" {
		return true
	}
	owner := executionOwner(tx, a)
	if id == owner.ID || id == owner.Repo || id == owner.Plan {
		return true
	}
	item, _ := tx.Item(id)
	if owner.Kind == "plan" && item.Plan == owner.ID {
		return true
	}
	return dependencyReadable(tx, owner, id)
}

func dependencyReadable(tx *Tx, owner Item, id string) bool {
	pending := append([]string{}, owner.Depends...)
	if owner.Kind == "plan" {
		for _, t := range tx.ItemsOfPlan(owner.ID) {
			pending = append(pending, t.Depends...)
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
		dep, _ := tx.Item(next)
		pending = append(pending, dep.Depends...)
	}
	return false
}

func readableUpdate(tx *Tx, a Actor, u Update) bool {
	if a.Role != "worker" {
		return true
	}
	if u.Item != "" {
		return readable(tx, a, u.Item)
	}
	for {
		if u.Author == a.Name {
			return true
		}
		parent, ok := tx.Update(u.ReplyTo)
		if !ok || u.ReplyTo >= u.ID {
			return false
		}
		u = parent
	}
}
