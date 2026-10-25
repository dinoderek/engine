package run

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/dinoderek/engine/internal/lint"
	"github.com/dinoderek/engine/internal/ps"
	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/tracker"
)

// Decide works out one pass: the coordinate reference's steps, in order. It
// reads only v and changes nothing.
func Decide(v View) []Action {
	f := v.Feature
	if !f.Open {
		return []Action{{Kind: Exit, Ref: f.Ref, Text: f.Ref.String() + " is closed"}}
	}
	if !v.TaskOnly {
		if acts, done := planStep(v); done {
			return acts
		}
	}
	var acts []Action
	acts = append(acts, deadSessions(v)...)
	acts = append(acts, stalls(v)...)
	acts = append(acts, reaps(v)...)
	placed := place(v, ready(v))
	acts = append(acts, placed...)
	if v.TaskOnly {
		switch t := f.Tasks[0]; {
		case !t.Open:
			acts = append(acts, Action{Kind: Exit, Ref: t.Ref, Text: t.Ref.String() + " is closed"})
		case t.Has(Failed):
			acts = append(acts, Action{Kind: Exit, Ref: t.Ref, Text: t.Ref.String() + " is " + Failed, Failure: true})
		}
		return acts
	}
	starting := slices.ContainsFunc(placed, func(a Action) bool { return a.Kind == StartTask })
	if l := featureLabel(v, starting); l != "" {
		acts = append(acts, Action{Kind: SetLabel, Ref: f.Ref, Label: l})
	}
	if len(f.Tasks) > 0 && !slices.ContainsFunc(f.Tasks, func(c tracker.Card) bool { return c.Open }) {
		acts = append(acts, Action{Kind: Finish, Ref: f.Ref, Text: "every task is closed"})
	}
	return acts
}

// planStep is the coordinate reference's step 2: start the planner, then
// lint its plan. Until the feature is planned, it is the whole pass.
func planStep(v View) ([]Action, bool) {
	f := v.Feature
	planner, started := v.Sessions[f.Ref]
	later := hasAny(f.Labels, []string{Planned, Executing, Verifying, Blocked, Done})
	switch {
	case later:
		return nil, false
	case !f.Has(Approved):
		return []Action{{Kind: Exit, Ref: f.Ref, Text: f.Ref.String() + " is not approved", Failure: true}}, true
	case started && planner.Unknown:
		return nil, true
	case started && planner.Live:
		if planner.waited(v.Now, StallWait) && planner.StallNoted.Before(planner.WaitingSince) {
			return []Action{{Kind: Stall, Ref: f.Ref, Session: planner,
				Text: fmt.Sprintf("the planner %s has waited for input for %s", planner.Name, StallWait)}}, true
		}
		return nil, true
	case len(f.Tasks) == 0 && started:
		if planner.StallNoted.Before(planner.Spawned) {
			return []Action{{Kind: Stall, Ref: f.Ref, Session: planner,
				Text: "the planner " + planner.Name + " ended without writing any task"}}, true
		}
		return nil, true
	case len(f.Tasks) == 0:
		if failed, ok := v.StartFailed[f.Ref]; ok && v.Now.Sub(failed) < StartRetry || v.Fleet.localFree() < 1 {
			return nil, true
		}
		return []Action{{Kind: StartPlanner, Ref: f.Ref, Text: "no tasks yet"}}, true
	case len(v.LintProblems) > 0 && !v.LintNoted:
		return []Action{{Kind: ReportLint, Ref: f.Ref, Text: "the plan fails lint"}}, true
	case len(v.LintProblems) > 0:
		return nil, true
	}
	return []Action{{Kind: SetLabel, Ref: f.Ref, Label: Planned}}, true
}

// deadSessions is step 6: a claimed open task whose session is dead fails.
func deadSessions(v View) []Action {
	var acts []Action
	for _, c := range v.Feature.Tasks {
		if row, ok := v.Rows[c.Ref]; ok && c.Open && hasAny(c.Labels, claimStates) && row.State == ps.Dead {
			acts = append(acts, Action{Kind: Fail, Ref: c.Ref, Text: "its session " + row.Session + " has died"})
		}
	}
	return acts
}

// stalls is step 4: a session that has not claimed in time, that died
// before claiming, or that waits outside a hard stop. Once per wait, and
// once per session that died.
func stalls(v View) []Action {
	var acts []Action
	for _, c := range v.Feature.Tasks {
		s, ok := v.Sessions[c.Ref]
		if !ok || !c.Open || s.Unknown {
			continue
		}
		if !s.Live {
			if !hasAny(c.Labels, takenStates) && s.StallNoted.Before(s.Spawned) {
				acts = append(acts, Action{Kind: Stall, Ref: c.Ref, Session: s,
					Text: s.Name + " ended before it claimed the task; it will be started again"})
			}
			continue
		}
		switch {
		case !hasAny(c.Labels, claimStates) && v.Now.Sub(s.Spawned) >= ClaimWait && s.StallNoted.Before(s.Spawned):
			acts = append(acts, Action{Kind: Stall, Ref: c.Ref, Session: s,
				Text: fmt.Sprintf("%s has not claimed the task %s after it started", s.Name, ClaimWait)})
		case hasAny(c.Labels, claimStates) && !c.Has(NeedsHuman) && s.waited(v.Now, StallWait) && s.StallNoted.Before(s.WaitingSince):
			acts = append(acts, Action{Kind: Stall, Ref: c.Ref, Session: s,
				Text: fmt.Sprintf("%s has waited for input for %s, outside a hard stop", s.Name, StallWait)})
		}
	}
	return acts
}

// reaps is step 7: close a finished hands-off session that left its window,
// once it has waited StallWait.
func reaps(v View) []Action {
	var acts []Action
	for _, c := range v.Feature.Tasks {
		s, ok := v.Sessions[c.Ref]
		if ok && finished(c, s) && s.waited(v.Now, StallWait) {
			acts = append(acts, Action{Kind: Reap, Ref: c.Ref, Session: s, Text: "its task is closed"})
		}
	}
	return acts
}

// finished reports whether c is a closed task with no state label whose
// hands-off session s is the one to close. A gated window is never closed.
func finished(c tracker.Card, s Session) bool {
	return !c.Open && s.Mode == "hands-off" && !hasAny(c.Labels, append(slices.Clone(takenStates), Merging))
}

// doneReaps is step 8's reaping, once the work is done: it closes every
// finished hands-off window that is waiting for input at once, and reports
// how many hands-off windows of closed tasks are still live, whether they
// are finished or not. A closed task's session on a host that cannot be
// read counts as live.
func doneReaps(v View) (acts []Action, live int) {
	for _, c := range v.Feature.Tasks {
		s, ok := v.Sessions[c.Ref]
		if ok && !c.Open && s.Unknown {
			live++
			continue
		}
		if !ok || c.Open || !s.Live || s.Mode != "hands-off" {
			continue
		}
		if finished(c, s) && s.Waiting {
			acts = append(acts, Action{Kind: Reap, Ref: c.Ref, Session: s, Text: "its task is closed and the work is done"})
			continue
		}
		live++
	}
	return acts, live
}

// ready is step 3: the ready tasks, in start order. A task whose window is
// live on any host is not ready: its session has not claimed yet. Nor is
// one whose session is on a host that cannot be read.
func ready(v View) []tracker.Card {
	var cands []tracker.Card
	for _, c := range v.Feature.Tasks {
		s := v.Sessions[c.Ref]
		blocked := slices.ContainsFunc(c.BlockedBy, func(b tracker.Blocker) bool { return b.Open })
		failed, ok := v.StartFailed[c.Ref]
		resting := ok && failed.After(s.Spawned) && v.Now.Sub(failed) < StartRetry
		if c.Open && !blocked && !hasAny(c.Labels, takenStates) && !s.Live && !s.Unknown && !resting && !v.Fleet.liveAnywhere(windowName(c.Ref)) {
			cands = append(cands, c)
		}
	}
	order(v.Feature.Tasks, cands)
	return cands
}

// windowName is the session name of ref's task, or "" for a bad repo.
func windowName(ref tracker.Ref) string {
	repo, err := session.ParseRepo(ref.Repo)
	if err != nil {
		return ""
	}
	return session.TaskName(repo, ref.Number)
}

// order sorts cands for starting: most open tasks downstream first, then
// deep before standard before light, then the lowest issue number.
func order(all, cands []tracker.Card) {
	edges := map[tracker.Ref][]tracker.Ref{}
	for _, c := range all {
		for _, b := range c.BlockedBy {
			edges[c.Ref] = append(edges[c.Ref], b.Ref)
		}
	}
	downstream := map[tracker.Ref]int{}
	for _, c := range all {
		if !c.Open {
			continue
		}
		for _, up := range lint.Upstream(edges, c.Ref) {
			downstream[up]++
		}
	}
	rank := map[string]int{"deep": 0, "standard": 1, "light": 2}
	tier := func(c tracker.Card) int {
		_, t, err := tracker.ParseHarnessTier(c.Body)
		if err != nil {
			return rank[tracker.DefaultTier]
		}
		return rank[t]
	}
	slices.SortStableFunc(cands, func(a, b tracker.Card) int {
		return cmp.Or(
			cmp.Compare(downstream[b.Ref], downstream[a.Ref]),
			cmp.Compare(tier(a), tier(b)),
			cmp.Compare(a.Ref.Repo, b.Ref.Repo),
			cmp.Compare(a.Ref.Number, b.Ref.Number))
	})
}

// featureLabel is step 5: the label the feature should carry once a task
// has started, or "" when it already does or none has.
func featureLabel(v View, starting bool) string {
	f := v.Feature
	started := starting
	open := 0
	var last tracker.Card
	for _, c := range f.Tasks {
		if !c.Open || hasAny(c.Labels, takenStates) {
			started = true
		}
		if c.Open {
			open++
			last = c
		}
	}
	if !started || open == 0 {
		return ""
	}
	want := Executing
	switch {
	case slices.ContainsFunc(f.Tasks, func(c tracker.Card) bool { return c.Open && c.Has(NeedsHuman) }):
		want = Blocked
	case open == 1 && isAcceptance(f.Tasks, last):
		want = Verifying
	}
	if f.Has(want) {
		return ""
	}
	return want
}

// isAcceptance reports whether c is blocked, directly or not, by every other
// task.
func isAcceptance(all []tracker.Card, c tracker.Card) bool {
	in := map[tracker.Ref]bool{}
	for _, t := range all {
		in[t.Ref] = true
	}
	edges := map[tracker.Ref][]tracker.Ref{}
	for _, t := range all {
		for _, b := range t.BlockedBy {
			if in[b.Ref] {
				edges[t.Ref] = append(edges[t.Ref], b.Ref)
			}
		}
	}
	up := 0
	for _, r := range lint.Upstream(edges, c.Ref) {
		if r != c.Ref {
			up++
		}
	}
	return len(all) > 1 && up == len(all)-1
}
