package reconcile

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"github.com/dinoderek/engine/internal/control"
)

// Decide works out one pass for v's root, in order: cleanup of a done root,
// a lint failure, the planner, and the tasks. The merge queue is decided
// before it, by DecideQueue.
func Decide(v View) []Action {
	var acts []Action
	switch {
	case v.Root.State == "done":
		return append(acts, cleanup(v)...)
	case len(v.Lint) > 0 && v.Root.State != "approved":
		return append(acts, notice(v.Root, fmt.Sprintf("lint/%d", v.Root.Version), "Plan lint failed: "+fmt.Sprint(v.Lint)))
	}
	if plan, done := planStep(v); done {
		return append(acts, plan...)
	}
	dead := deadSessions(v)
	acts = append(acts, dead...)
	v.Fleet = v.Fleet.dyingAtLimit(v, dead)
	acts = append(acts, releaseStopped(v)...)
	acts = append(acts, releaseMoved(v)...)
	acts = append(acts, moveWaits(v)...)
	acts = append(acts, stalls(v)...)
	acts = append(acts, reaps(v)...)
	acts = append(acts, limitNotes(v)...)
	placed := place(v)
	acts = append(acts, placed...)
	if len(v.Tasks) > 0 && !slices.ContainsFunc(v.Tasks, Item.open) {
		acts = append(acts, Action{Kind: SetState, Item: v.Root.ID, State: "done", Key: "finished", Text: summary(v)})
	}
	return acts
}

func notice(t Item, key, text string) Action {
	return Action{Kind: Notice, Item: t.ID, Key: key, Text: text}
}

// planStep starts the planner, then lints its plan. Until the plan is
// planned, it is the whole pass. A planner that died without writing a task
// is ended, and the next pass starts a new one.
func planStep(v View) ([]Action, bool) {
	p := v.Root
	planner := p.Session
	if p.Current.State == "ended" {
		planner = nil
	}
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
		return []Action{plannerEnd(p)}, true
	case len(v.Tasks) == 0:
		return plannerStart(v), true
	case len(v.Lint) > 0:
		return []Action{notice(p, fmt.Sprintf("lint/%d", p.Version), "Plan lint failed; no tasks were dispatched. Fix the cards and restart coordination.")}, true
	}
	return []Action{{Kind: SetState, Item: p.ID, State: "planned"}}, true
}

// plannerEnd ends the attempt of plan p's planner, whose window is gone.
func plannerEnd(p Item) Action {
	s := *p.Session
	a := Action{Kind: EndAttempt, Item: p.ID, Attempt: p.Current.ID, Text: "the planner " + s.Name + " has died", Tail: s.PaneTail}
	if s.DiedAtLimit {
		a.Text = "the planner " + s.Name + " died at a usage limit"
		a.AtLimit, a.ResetsAt = true, s.ResetsAt
	}
	return a
}

// plannerStart starts a planner on a plan with no task, unless the plan has
// an open question of its own, its last planner died at a usage limit that
// has not reset, or this host has no free slot.
func plannerStart(v View) []Action {
	if v.Root.Question > 0 || plannerLimited(v.Root.Current, v.Now) || v.Fleet.LocalFree() < 1 {
		return nil
	}
	return []Action{plannerDispatch(v)}
}

// plannerLimited reports whether at, a planner's ended attempt, died at a
// usage limit that still holds: until ResumeDelay after a known reset, or
// for ProbeEvery after the death when the reset is unknown.
func plannerLimited(at control.Attempt, now time.Time) bool {
	if !at.Limited {
		return false
	}
	if KnownReset(at.ResetsAt, at.Ended) {
		return now.Before(time.Unix(at.ResetsAt, 0).Add(ResumeDelay))
	}
	return now.Before(at.Ended.Add(ProbeEvery))
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

// deadSessions ends the attempt of a held open task whose session is dead:
// at a usage limit when its run's limit marker is set.
func deadSessions(v View) []Action {
	var acts []Action
	for _, t := range v.Tasks {
		s := t.Session
		if s == nil || !t.open() || !t.claimed() || s.Live || s.Unknown {
			continue
		}
		a := Action{Kind: EndAttempt, Item: t.ID, Attempt: t.Current.ID, Text: "its session " + s.Name + " has died", Tail: s.PaneTail}
		if s.DiedAtLimit {
			a.Text = "its session " + s.Name + " died at a usage limit"
			a.AtLimit, a.ResetsAt, a.Probe = true, s.ResetsAt, v.Fleet.probe(t)
		}
		acts = append(acts, a)
	}
	return acts
}

// releaseStopped releases the window an operator's stop left on a host, keeping the
// worktree and branch for the resume. It repeats each pass until a read shows
// the window gone, so a release that failed is retried.
func releaseStopped(v View) []Action {
	var acts []Action
	for _, t := range v.Tasks {
		if s := t.Session; s != nil && t.Stopped && (s.Live || s.DeadWindow) {
			acts = append(acts, Action{Kind: Release, Item: t.ID, Attempt: t.Current.ID, Session: *s, Stopped: true, Text: "the operator stopped its task"})
		}
	}
	return acts
}

// moveWaits tells the operator, once per move, that a moved task waits for
// its old host, which could not be read, to release its old session.
func moveWaits(v View) []Action {
	var acts []Action
	for _, t := range v.Tasks {
		if t.open() && len(t.Strays) == 0 && t.Current.Reason == control.EndMoved && v.moveHeld(t) {
			acts = append(acts, notice(t, "move-wait/"+t.Current.ID, fmt.Sprintf(
				"%s waits for host %s, which could not be read, to release its old session before it resumes elsewhere. "+
					"`%s` sends it on without waiting; the old session may then still push.", t.ID, t.Current.Host, moveCommand(t))))
		}
	}
	return acts
}

// moveCommand is the command that moves t again to where its move sends it,
// confirmed.
func moveCommand(t Item) string {
	text := "engine task move " + t.ID
	if h := t.Current.MoveHost; h != "" {
		text += " --host " + h
	}
	if h := t.Current.MoveHarness; h != "" {
		text += " --harness " + h
	}
	return text + " --yes"
}

// releaseMoved releases each window a moved attempt left on a host that
// answered, pushing its branch first and keeping the worktree and branch. It
// repeats each pass until the release succeeds, so the task's next dispatch
// waits for it (moveHeld). A finished task's is only closed, as Stopped: its
// branch is not pushed again.
func releaseMoved(v View) []Action {
	var acts []Action
	for _, t := range v.Tasks {
		for _, s := range t.Strays {
			acts = append(acts, Action{Kind: Release, Item: t.ID, Attempt: s.Attempt.ID, Session: s.Session, Moved: true,
				Stopped: t.State == "done" || t.State == "failed", Text: "the operator moved its task"})
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
		if a, ok := probeHit(v, t); ok {
			acts = append(acts, a)
			continue
		}
		if t.open() && !t.Stopped {
			acts = append(acts, limit(v, t)...)
		}
		s := t.Session
		if s == nil || !t.open() || t.Stopped || s.Unknown || s.Limited || s.Waiting && s.ResetsAt > 0 {
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
			text := fmt.Sprintf("%s has not claimed the task %s after it started", s.Name, ClaimWait)
			if s.AtDialog() {
				text = DialogText(text)
			}
			acts = append(acts, stall(t, s.Spawned, text)...)
		case t.claimed() && t.state() != "needs-human" && t.Question == 0 && s.Waited(v.Now, StallWait) && !s.Flagged(s.WaitingSince):
			acts = append(acts, stall(t, s.WaitingSince, WaitText(*s, fmt.Sprintf("%s has waited for input for %s, outside a hard stop", s.Name, StallWait)))...)
		}
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
