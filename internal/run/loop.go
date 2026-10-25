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
// passes it checks this host's sessions every CheckEvery, and passes again
// early when one has died, closed, or crossed a wait. A pass that cannot read
// its view is logged and tried again at the next check. At done it winds
// down before it returns. It returns an error when the run ends short of
// done.
func Loop(w World, ref tracker.Ref, taskOnly bool, sleep func(time.Duration)) error {
	for {
		v, err := Gather(w, ref, taskOnly)
		if err != nil {
			w.Logf("pass: %v", err)
			sleep(CheckEvery)
			continue
		}
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
		waitForPass(w, v, sleep)
	}
}

// waitForPass sleeps until PassEvery has gone by, or a check of the host says
// a pass is due.
func waitForPass(w World, v View, sleep func(time.Duration)) {
	for waited := time.Duration(0); waited < PassEvery; waited += CheckEvery {
		sleep(CheckEvery)
		if passDue(w, v) {
			return
		}
	}
}

// passDue re-reads only this host's sessions: a pass is due when one has
// started, stopped or changed between working and waiting, or when the new
// sessions alone would stall or reap one.
func passDue(w World, v View) bool {
	windows, err := w.Windows()
	if err != nil {
		w.Logf("check: %v", err)
		return false
	}
	now := w.Now()
	fresh, err := sessions(w, v.Feature, v.TaskOnly, windows, now)
	if err != nil {
		w.Logf("check: %v", err)
		return false
	}
	for ref, s := range fresh {
		old, ok := v.Sessions[ref]
		if !ok || old.Live != s.Live || old.Waiting != s.Waiting {
			return true
		}
	}
	next := v
	next.Sessions, next.Now = fresh, now
	return slices.ContainsFunc(Decide(next), func(a Action) bool { return a.Kind == Stall || a.Kind == Reap })
}

// windDown runs after the work is done. Every CheckEvery it closes each
// finished hands-off window that is waiting for input, and it returns when
// none of the closed tasks has a hands-off window left, or after DoneWait.
// A window a task's executor still works in is left to close itself.
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
