package reconcile

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/dinoderek/engine/internal/control"
)

// place dispatches the ready tasks, in start order, on the hosts placement
// picks. A task no host could take even with every slot free is a notice.
func place(v View) []Action {
	cands := ready(v)
	wants := make([]Want, len(cands))
	byID := map[string]Item{}
	for i, t := range cands {
		wants[i] = Want{ID: t.ID, Requires: t.Requires, Harness: t.Harness}
		byID[t.ID] = t
	}
	var acts []Action
	for _, p := range v.Fleet.Place(wants) {
		t := byID[p.ID]
		switch {
		case p.Wait == "":
			acts = append(acts, Action{Kind: Dispatch, Item: t.ID, Host: p.Host, Harness: p.Harness, Resume: t.Resume, Text: "ready, on " + p.Host})
		case p.Notify:
			acts = append(acts, notice(t, fmt.Sprintf("placement/%d", t.Version), p.Wait))
		}
	}
	return acts
}

// ready are the tasks to dispatch, in start order. A task whose session is
// live, or unknown, is not ready; nor is one whose window is live on any
// host that answered, as an earlier attempt's may be.
func ready(v View) []Item {
	var cands []Item
	for _, t := range v.Tasks {
		s := t.observed()
		if t.open() && len(t.Blocked) == 0 && !t.taken() && !s.Live && !s.Unknown && !v.Fleet.LiveAnywhere(t.Name) {
			cands = append(cands, t)
		}
	}
	order(v.Tasks, cands)
	return cands
}

// order sorts cands for starting: most open tasks downstream first, then
// deep before standard before light, then the lowest number.
func order(all, cands []Item) {
	downstream := map[string]int{}
	edges := dependencies(all, false)
	for _, t := range all {
		if !t.open() {
			continue
		}
		for _, up := range upstream(edges, t.ID) {
			downstream[up]++
		}
	}
	rank := map[string]int{"deep": 0, "standard": 1, "light": 2}
	tier := func(t Item) int { return rank[cmp.Or(t.Tier, "standard")] }
	slices.SortStableFunc(cands, func(a, b Item) int {
		return cmp.Or(
			cmp.Compare(downstream[b.ID], downstream[a.ID]),
			cmp.Compare(tier(a), tier(b)),
			cmp.Compare(control.Number(a.ID), control.Number(b.ID)))
	})
}

// dependencies maps each task to the tasks it depends on: every one, or,
// with within, only those among all.
func dependencies(all []Item, within bool) map[string][]string {
	in := map[string]bool{}
	for _, t := range all {
		in[t.ID] = true
	}
	edges := map[string][]string{}
	for _, t := range all {
		for _, d := range t.Depends {
			if !within || in[d] {
				edges[t.ID] = append(edges[t.ID], d)
			}
		}
	}
	return edges
}

// upstream returns every task that from depends on, directly or through
// others.
func upstream(edges map[string][]string, from string) []string {
	seen := map[string]bool{}
	var out []string
	stack := slices.Clone(edges[from])
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
		stack = append(stack, edges[id]...)
	}
	return out
}

// planState is the state the plan should be in once a task has started,
// or "" when it already is or none has.
func planState(v View, starting bool) string {
	started := starting
	open := 0
	var last Item
	for _, t := range v.Tasks {
		if !t.open() || t.taken() {
			started = true
		}
		if t.open() {
			open++
			last = t
		}
	}
	if !started || open == 0 {
		return ""
	}
	want := "executing"
	switch {
	case slices.ContainsFunc(v.Tasks, func(t Item) bool { return t.open() && t.state() == "needs-human" }):
		want = "blocked"
	case open == 1 && isAcceptance(v.Tasks, last):
		want = "verifying"
	}
	if v.Root.State == want {
		return ""
	}
	return want
}

// isAcceptance reports whether t depends, directly or not, on every other
// task.
func isAcceptance(all []Item, t Item) bool {
	up := slices.DeleteFunc(upstream(dependencies(all, true), t.ID), func(id string) bool { return id == t.ID })
	return len(all) > 1 && len(up) == len(all)-1
}
