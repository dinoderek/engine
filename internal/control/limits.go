package control

import (
	"cmp"
	"slices"
	"time"
)

// HarnessLimit is a host × harness whose account is at a usage limit, as the
// attempts there record it: one ended at a limit (Attempt.Limited), and no
// task's attempt started there since has claimed its task. A planner's does
// not count: placement never gates it, so it may sit live while limited.
type HarnessLimit struct {
	Host    string
	Harness string
	// ResetsAt is the newest limit's reset in epoch seconds, or zero when
	// unknown.
	ResetsAt int64
	// Ended is when the newest limit ended its attempt, and Since when the
	// first of the run of limits that no claimed attempt broke did: how
	// long the account has been out.
	Ended time.Time
	Since time.Time
	// Attempt and Task are the newest limited attempt and its item.
	Attempt string
	Task    string
	// Probe is the start of the newest attempt started there after Ended,
	// zero when none has; Live is true while one of them has not ended.
	Probe time.Time
	Live  bool
	// Probes counts the attempts started there since Since, the limited
	// ones after the first included: each one tried the account and failed.
	Probes int
}

// HarnessLimits are the host × harness pairs at a usage limit, by host, then
// harness.
func HarnessLimits(st *State) []HarnessLimit {
	wrote := authors(st)
	pairs := map[[2]string][]Attempt{}
	for _, at := range st.Attempts {
		if at.Host != "" && at.Harness != "" && st.Items[at.Item].Kind == "task" {
			key := [2]string{at.Host, at.Harness}
			pairs[key] = append(pairs[key], at)
		}
	}
	var out []HarnessLimit
	for key, attempts := range pairs {
		if l, ok := harnessLimit(wrote, key[0], key[1], attempts); ok {
			out = append(out, l)
		}
	}
	slices.SortFunc(out, func(a, b HarnessLimit) int {
		return cmp.Or(cmp.Compare(a.Host, b.Host), cmp.Compare(a.Harness, b.Harness))
	})
	return out
}

// harnessLimit is one pair's limit, from its attempts, if it has one.
func harnessLimit(wrote map[string]bool, host, harness string, attempts []Attempt) (HarnessLimit, bool) {
	var limits []Attempt
	var claim time.Time // the newest start of an attempt that claimed its task
	for _, at := range attempts {
		if at.Limited && at.State == "ended" {
			limits = append(limits, at)
		}
		if claimedBy(wrote, at) && at.Started.After(claim) {
			claim = at.Started
		}
	}
	if len(limits) == 0 {
		return HarnessLimit{}, false
	}
	slices.SortFunc(limits, func(a, b Attempt) int { return cmp.Or(a.Ended.Compare(b.Ended), Number(a.ID)-Number(b.ID)) })
	newest := limits[len(limits)-1]
	if claim.After(newest.Ended) {
		return HarnessLimit{}, false
	}
	l := HarnessLimit{Host: host, Harness: harness, ResetsAt: newest.ResetsAt, Ended: newest.Ended, Since: newest.Ended, Attempt: newest.ID, Task: newest.Item}
	for _, at := range limits {
		if at.Ended.After(claim) {
			l.Since = at.Ended
			break
		}
	}
	for _, at := range attempts {
		if at.Started.After(l.Since) {
			l.Probes++
		}
		if at.Started.After(l.Ended) {
			l.Probe = later(l.Probe, at.Started)
			l.Live = l.Live || at.State != "ended"
		}
	}
	return l, true
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
