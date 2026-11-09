package reconcile

import (
	"cmp"
	"fmt"
	"slices"
	"time"
)

// Decide works out one pass for v's root, in order: merges first, since
// their outcome changes the rest of the pass, then cleanup of a done root,
// a lint failure, the planner, and the tasks.
func Decide(v View) []Action {
	acts := merges(v)
	switch {
	case v.Root.State == "done":
		return append(acts, cleanup(v)...)
	case len(v.Lint) > 0 && v.Root.State != "approved":
		return append(acts, notice(v.Root, fmt.Sprintf("lint/%d", v.Root.Version), "Plan lint failed: "+fmt.Sprint(v.Lint)))
	}
	if v.Root.Kind == "plan" {
		if plan, done := planStep(v); done {
			return append(acts, plan...)
		}
	}
	acts = append(acts, deadSessions(v)...)
	acts = append(acts, stalls(v)...)
	acts = append(acts, reaps(v)...)
	placed := place(v)
	acts = append(acts, placed...)
	if v.Root.Kind == "task" {
		if v.Root.state() == "failed" {
			acts = append(acts, Action{Kind: Stop, Item: v.Root.ID, Text: v.Root.ID + " is failed"})
		}
		return acts
	}
	if len(v.Tasks) > 0 && !slices.ContainsFunc(v.Tasks, Item.open) {
		acts = append(acts, Action{Kind: SetState, Item: v.Root.ID, State: "done", Key: "finished", Text: summary(v)})
	}
	return acts
}

func notice(t Item, key, text string) Action {
	return Action{Kind: Notice, Item: t.ID, Key: key, Text: text}
}

// planStep starts the planner, then lints its plan. Until the plan is
// planned, it is the whole pass.
func planStep(v View) ([]Action, bool) {
	p := v.Root
	planner := p.Session
	switch {
	case p.State == "planned" || p.State == "done":
		return nil, false
	case p.State != "approved":
		return []Action{{Kind: Stop, Item: p.ID, Text: p.ID + " is not approved"}}, true
	case planner != nil && planner.Unknown:
		return nil, true
	case planner != nil && planner.Live:
		if acts := limit(v, p); len(acts) > 0 {
			return acts, true
		}
		if planner.Waited(v.Now, StallWait) && !planner.Flagged(planner.WaitingSince) {
			return stall(p, planner.WaitingSince, WaitText(*planner, "the planner "+planner.Name+" has waited for input for "+StallWait.String())), true
		}
		return nil, true
	case len(v.Tasks) == 0 && planner != nil:
		if !planner.Flagged(planner.Spawned) {
			return stall(p, planner.Spawned, "the planner "+planner.Name+" ended without writing any task"), true
		}
		return nil, true
	case len(v.Tasks) == 0:
		if v.Fleet.LocalFree() < 1 {
			return nil, true
		}
		return []Action{plannerDispatch(v)}, true
	case len(v.Lint) > 0:
		return []Action{notice(p, fmt.Sprintf("lint/%d", p.Version), "Plan lint failed; no tasks were dispatched. Fix the cards and restart coordination.")}, true
	}
	return []Action{{Kind: SetState, Item: p.ID, State: "planned"}}, true
}

// plannerDispatch starts the planner on this host, with the plan's harness
// or the default. A harness this host does not run, or one signed out here,
// is a notice instead.
func plannerDispatch(v View) Action {
	p := v.Root
	host := v.Fleet.Local
	harness := cmp.Or(p.Harness, v.Fleet.DefaultHarness)
	local, _ := v.Fleet.Host(host)
	if _, ok := v.Fleet.Caps[harness]; ok && slices.Contains(local.Harnesses, harness) {
		if local.SignedOut(harness) {
			return notice(p, fmt.Sprintf("planner-signin/%d/%s", p.Version, harness),
				"The planner waits: "+signedOutText(host, harness)+". It starts once the harness is signed in.")
		}
		return Action{Kind: Dispatch, Item: p.ID, Host: host, Harness: harness, Resume: p.Resume, Text: "no tasks yet"}
	}
	return notice(p, fmt.Sprintf("planner-harness/%d", p.Version), fmt.Sprintf(
		"The planner was not started: planner harness %s is not one host %s runs. Edit the plan's harness or the host config.", harness, host))
}

// stall flags a session once per start or wait. A task waiting on its merge
// waits on the coordinator, not outside the protocol, and is not flagged.
func stall(t Item, at time.Time, text string) []Action {
	if t.Merge != nil {
		return nil
	}
	a := notice(t, fmt.Sprintf("stall/%s/%d", t.Current.ID, at.Unix()), text)
	a.Attempt = t.Current.ID
	a.Stalled = at
	return []Action{a}
}

// limit is a limited session's resume or alert, unless it is at a hard
// stop.
func limit(v View, t Item) []Action {
	if t.state() == "needs-human" || t.Session == nil {
		return nil
	}
	s := *t.Session
	step, text := Limit(s, v.Now)
	switch step {
	case LimitResume:
		return []Action{{Kind: Resume, Item: t.ID, Attempt: t.Current.ID, ResetsAt: s.ResetsAt, Session: s, Text: text}}
	case LimitFar, LimitStuck:
		kind := map[LimitStep]string{LimitFar: "far", LimitStuck: "stuck"}[step]
		a := notice(t, fmt.Sprintf("limit/%s/%s/%d", kind, t.Current.ID, s.ResetsAt), text)
		a.Attempt, a.Limit, a.Far = t.Current.ID, s.ResetsAt, step == LimitFar
		return []Action{a}
	}
	return nil
}

// deadSessions ends the attempt of a held open task whose session is dead.
func deadSessions(v View) []Action {
	var acts []Action
	for _, t := range v.Tasks {
		s := t.Session
		if s != nil && t.open() && t.claimed() && !s.Live && !s.Unknown {
			acts = append(acts, Action{Kind: EndAttempt, Item: t.ID, Attempt: t.Current.ID, Text: "its session " + s.Name + " has died"})
		}
	}
	return acts
}

// stalls flags a session that has not claimed in time, that died before
// claiming, or that waits outside a hard stop: once per wait, and once per
// session that died. A limited session gets its limit step instead.
func stalls(v View) []Action {
	var acts []Action
	for _, t := range v.Tasks {
		if t.open() {
			acts = append(acts, limit(v, t)...)
		}
		s := t.Session
		if s == nil || !t.open() || s.Unknown || s.Limited || s.Waiting && s.ResetsAt > 0 {
			continue
		}
		if !s.Live {
			if !t.taken() && !s.Flagged(s.Spawned) {
				acts = append(acts, stall(t, s.Spawned, s.Name+" ended before it claimed the task; it will be started again")...)
			}
			continue
		}
		switch {
		case !t.claimed() && v.Now.Sub(s.Spawned) >= ClaimWait && !s.Flagged(s.Spawned):
			acts = append(acts, stall(t, s.Spawned, fmt.Sprintf("%s has not claimed the task %s after it started", s.Name, ClaimWait))...)
		case t.claimed() && t.state() != "needs-human" && t.Question == 0 && s.Waited(v.Now, StallWait) && !s.Flagged(s.WaitingSince):
			acts = append(acts, stall(t, s.WaitingSince, WaitText(*s, fmt.Sprintf("%s has waited for input for %s, outside a hard stop", s.Name, StallWait)))...)
		}
	}
	return acts
}

// reaps releases a done task's session once it has been idle StallWait,
// and once a merged one has had time to report.
func reaps(v View) []Action {
	var acts []Action
	for _, t := range v.Tasks {
		s := t.Session
		if s != nil && !t.open() && s.IdleFor(v.Now, StallWait) && !reporting(v, t) {
			acts = append(acts, release(t, "its task is done"))
		}
	}
	return acts
}

// release closes t's session. One that only a background job keeps busy
// is closed with that job still running, so the operator is told.
func release(t Item, why string) Action {
	s := *t.Session
	a := Action{Kind: Release, Item: t.ID, Attempt: t.Current.ID, Session: s, Text: why}
	if s.Background {
		a.Key = "released/" + t.Current.ID
		a.Text = fmt.Sprintf("%s was released, with a background job still running, because %s", s.Name, why)
	}
	return a
}

// reporting reports whether t was merged too recently for its session to
// have reported.
func reporting(v View, t Item) bool {
	return !t.MergedAt.IsZero() && v.Now.Sub(t.MergedAt) < ReportWindow
}

// cleanup, once the root is done, releases every session that is idle or
// gone, and stops coordinating when none is left busy in a turn, limited,
// unknown, or reporting a merge.
func cleanup(v View) []Action {
	var acts []Action
	pending := false
	for _, t := range v.Sessions() {
		s := *t.Session
		switch {
		case t.Current.State == "ended":
		case s.Unknown || s.Live && !s.Idle() || reporting(v, t):
			pending = true
		default:
			acts = append(acts, release(t, "the work is done"))
		}
	}
	if !pending {
		acts = append(acts, Action{Kind: Stop, Item: v.Root.ID, Text: v.Root.ID + " is done"})
	}
	return acts
}

// summary is the done plan's notice: each task and its PR.
func summary(v View) string {
	text := "Every task is complete."
	for _, t := range v.Tasks {
		text += fmt.Sprintf("\n%s: %s %s", t.ID, t.Title, t.PR)
	}
	return text
}
