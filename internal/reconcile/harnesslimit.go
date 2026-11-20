package reconcile

import (
	"fmt"
	"slices"
	"time"

	"github.com/dinoderek/engine/internal/control"
)

// The waits a usage limit on a host × harness sets.
const (
	// ProbeEvery is how often a host × harness at a limit with no known
	// reset is tried with one task.
	ProbeEvery = 30 * time.Minute
	// ProbeNote is how long such a limit lasts, through failed probes, before
	// the operator is told once.
	ProbeNote = 12 * time.Hour
)

// pair is a host and a harness.
type pair struct{ host, harness string }

// limitFor is the usage limit on host × harness, if it has one.
func (f Fleet) limitFor(host, harness string) (control.HarnessLimit, bool) {
	i := slices.IndexFunc(f.Limits, func(l control.HarnessLimit) bool { return l.Host == host && l.Harness == harness })
	if i < 0 {
		return control.HarnessLimit{}, false
	}
	return f.Limits[i], true
}

// gate is what host × harness's usage limit lets placement start now: any
// task, none, or one, a probe of an account whose reset is unknown. A known
// reset opens it ResumeDelay after the reset, as the in-session continue
// waits. An unknown one lets one task through ProbeEvery after the limit or
// the last probe, while no probe still runs. A reset no later than the limit
// itself, stale or from a skewed clock, counts as unknown, so a wrong one
// never sends a task each pass. Without a limit it is open.
func (f Fleet) gate(host, harness string) (open, one bool, why string) {
	l, ok := f.limitFor(host, harness)
	if !ok {
		return true, false, ""
	}
	if KnownReset(l.ResetsAt, l.Ended) {
		at := time.Unix(l.ResetsAt, 0).Add(ResumeDelay)
		if !f.Now.Before(at) {
			return true, false, ""
		}
		return false, false, fmt.Sprintf("%s on %s is at a usage limit until %s", harness, host, at.UTC().Format(time.RFC3339))
	}
	if l.Live {
		return false, false, fmt.Sprintf("%s on %s is at a usage limit with no known reset; a probe is running", harness, host)
	}
	next := l.Ended
	if l.Probe.After(next) {
		next = l.Probe
	}
	if next = next.Add(ProbeEvery); f.Now.Before(next) {
		return false, false, fmt.Sprintf("%s on %s is at a usage limit with no known reset; the next probe is at %s", harness, host, next.UTC().Format(time.RFC3339))
	}
	return true, true, ""
}

// KnownReset reports whether resetsAt, a limit's reset in epoch seconds,
// is known: later than ended, when the limit ended its attempt. Zero, a stale
// reset or a skewed clock's is unknown.
func KnownReset(resetsAt int64, ended time.Time) bool {
	return resetsAt > ended.Unix()
}

// limitWait says why host × harness takes no task now for its usage limit,
// or nothing.
func (f Fleet) limitWait(host, harness string) string {
	_, _, why := f.gate(host, harness)
	return why
}

// probe reports whether t's current attempt started on a host × harness
// already at a usage limit: a dispatch after the limit, which tries it.
func (f Fleet) probe(t Item) bool {
	l, ok := f.limitFor(t.Current.Host, t.Current.Harness)
	return ok && t.Current.Started.After(l.Ended)
}

// probeHit is the release of a session that tried a host × harness at a
// usage limit and hit the limit again before it claimed its task: its
// kickoff turn did no work, so it ends at once, keeping the worktree and
// branch. Once its window is gone, its death at the limit records the new
// reset, and the wait starts again.
func probeHit(v View, t Item) (Action, bool) {
	s := t.Session
	if s == nil || !t.open() || t.Stopped || t.Claimed || !s.Live || s.Unknown || !s.Limited || !v.Fleet.probe(t) {
		return Action{}, false
	}
	return Action{Kind: Release, Item: t.ID, Session: *s, Stopped: true,
		Text: s.Name + " hit the usage limit again in its kickoff turn"}, true
}

// limitNotes tell the operator, once per limit, that a host × harness has
// been at a usage limit with no known reset for ProbeNote, through failed
// probes. Every plan's pass may send it: its key makes it one note.
func limitNotes(v View) []Action {
	var acts []Action
	for _, l := range v.Fleet.Limits {
		if KnownReset(l.ResetsAt, l.Ended) || l.Probes == 0 || v.Now.Sub(l.Since) < ProbeNote {
			continue
		}
		acts = append(acts, notice(Item{}, fmt.Sprintf("limit-probes/%s/%s/%d", l.Host, l.Harness, l.Since.Unix()), fmt.Sprintf(
			"%s on %s has been at a usage limit with no known reset since %s, and every probe since has hit it; "+
				"its tasks wait, or start on another host or harness with quota. Check that account's usage on %s.",
			l.Harness, l.Host, l.Since.UTC().Format(time.RFC3339), l.Host)))
	}
	return acts
}
