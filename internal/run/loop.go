package run

import (
	"errors"
	"slices"
	"time"

	"github.com/dinoderek/engine/internal/tracker"
)

// The loop's timing.
const (
	// PassEvery is the backstop: a full pass at least this often.
	PassEvery = 5 * time.Minute
	// CheckEvery is how often the host alone is checked between passes.
	CheckEvery = time.Minute
)

// Loop runs passes over ref until the feature, or the task, is over. Between
// passes it checks every host's sessions every CheckEvery, and passes again
// early when one has died, closed, or crossed a wait. A pass that cannot read
// its view is logged and tried again at the next check. At done it winds
// down before it returns. It returns an error when the run ends short of
// done.
func Loop(w World, ref tracker.Ref, taskOnly bool, sleep func(time.Duration)) error {
	down := outages{}
	for {
		v, err := Gather(w, ref, taskOnly)
		if err != nil {
			w.Logf("pass: %v", err)
			sleep(CheckEvery)
			continue
		}
		down.note(w, v.Fleet)
		acts := Decide(v)
		if Apply(w, v, acts) {
			for _, a := range acts {
				if a.Kind == Exit && a.Failure {
					return errors.New(a.Text)
				}
			}
			w.Logf("done: %s", ref)
			windDown(w, ref, taskOnly, sleep)
			return nil
		}
		waitForPass(w, v, down, sleep)
	}
}

// outages are the hosts the run has said it cannot read, so that an outage
// is logged once when it starts and once when it ends, not at every check.
type outages map[string]bool

func (o outages) note(w World, f Fleet) {
	for _, h := range f.Hosts {
		switch {
		case h.Unreachable != "" && !o[h.Name]:
			o[h.Name] = true
			w.Logf("host %s cannot be read, so its sessions are unknown and it takes no task: %s", h.Name, h.Unreachable)
		case h.Unreachable == "" && o[h.Name]:
			delete(o, h.Name)
			w.Logf("host %s answers again", h.Name)
		}
	}
}

// waitForPass sleeps until PassEvery has gone by, or a check of the hosts
// says a pass is due.
func waitForPass(w World, v View, down outages, sleep func(time.Duration)) {
	for waited := time.Duration(0); waited < PassEvery; waited += CheckEvery {
		sleep(CheckEvery)
		if passDue(w, v, down) {
			return
		}
	}
}

// passDue re-reads every host's sessions, one status call each, and never
// the tracker: a pass is due when a session has started, stopped, become
// known or unknown, or changed between working and waiting, or when the new
// sessions alone would stall or reap one.
func passDue(w World, v View, down outages) bool {
	next := v
	next.Fleet, next.Now = w.Fleet(), w.Now()
	down.note(w, next.Fleet)
	fresh, err := sessions(w, next)
	if err != nil {
		w.Logf("check: %v", err)
		return false
	}
	for ref, s := range fresh {
		old, ok := v.Sessions[ref]
		if !ok || old.Live != s.Live || old.Waiting != s.Waiting || old.Limited != s.Limited || old.ResetsAt != s.ResetsAt || old.Unknown != s.Unknown {
			return true
		}
	}
	next.Sessions = fresh
	return slices.ContainsFunc(Decide(next), func(a Action) bool { return a.Kind == Stall || a.Kind == Reap })
}

// windDown runs after the work is done. Every CheckEvery it releases each
// finished hands-off window that is waiting for input, and it returns when
// none of the closed tasks has a hands-off window left, or after DoneWait.
// A window a task's executor still works in is left to close itself, and
// one on a host that cannot be read is waited for.
func windDown(w World, ref tracker.Ref, taskOnly bool, sleep func(time.Duration)) {
	for waited := time.Duration(0); ; waited += CheckEvery {
		v, err := Gather(w, ref, taskOnly)
		if err != nil {
			w.Logf("wind down: %v", err)
		} else {
			acts, live := doneReaps(v)
			Apply(w, v, acts)
			if live == 0 {
				return
			}
		}
		if waited >= DoneWait {
			w.Logf("wind down: %s is done, and windows of its closed tasks are still open", ref)
			return
		}
		sleep(CheckEvery)
	}
}
