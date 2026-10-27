package run

import (
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/session"
)

func TestALimitedSessionNeverStalls(t *testing.T) {
	for _, claimed := range []bool{false, true} {
		w := onDevbox(t, behaviour{})
		e := w.execs[ref(2)]
		if claimed {
			w.card(ref(2)).Labels = []string{Running}
		}
		// Start with an ordinary wait, so an existing timer must be cleared.
		e.status = session.AgentWaiting
		passOf(t, w)
		e.limited = true
		e.resetsAt = t0.Add(time.Hour).Unix()
		checks(t, w, 30)
		s := gather(t, w).Sessions[ref(2)]
		if !s.Live || !s.Limited || s.Waiting || s.ResetsAt != t0.Add(time.Hour).Unix() {
			t.Errorf("session %+v", s)
		}
		if noteOf(t, w, 2, WaitingSince) || len(w.comments[ref(2)]) != 0 || len(w.starts) != 1 {
			t.Errorf("limited session stalled: comments %q, starts %v", w.comments[ref(2)], w.starts)
		}
		if claimed {
			// Clearing the limit begins a fresh wait; it cannot inherit the old one.
			e.limited = false
			e.resetsAt = 0
			e.status = session.AgentBusy
			checks(t, w, 1)
			e.status = session.AgentWaiting
			checks(t, w, 1)
			if len(w.comments[ref(2)]) != 0 {
				t.Fatal("inherited an expired wait")
			}
			checks(t, w, 11)
			if len(w.comments[ref(2)]) != 1 {
				t.Errorf("ordinary wait not reported: %q", w.comments[ref(2)])
			}
		}
	}
}

func TestLimitedChangesTriggerACheck(t *testing.T) {
	w := onDevbox(t, behaviour{claim: 1})
	v := gather(t, w)
	w.execs[ref(2)].limited = true
	if !passDue(w, v, outages{}) {
		t.Error("limit did not trigger a pass")
	}
	v = gather(t, w)
	w.execs[ref(2)].limited = false
	if !passDue(w, v, outages{}) {
		t.Error("resume did not trigger a pass")
	}
}
