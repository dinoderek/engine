package run

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/tracker"
)

func TestAutomaticRecoveryLeavesNeedsHumanTasksAlone(t *testing.T) {
	for _, waiting := range []bool{false, true} {
		for _, taskOnly := range []bool{false, true} {
			w := limitedWorld(t, waiting)
			w.card(ref(2)).Labels = []string{NeedsHuman}
			w.now = t0.Add(6 * time.Minute)
			v, err := Gather(w, map[bool]tracker.Ref{false: ref(1), true: ref(2)}[taskOnly], taskOnly)
			if err != nil {
				t.Fatal(err)
			}
			acts := Decide(v)
			for _, a := range acts {
				if a.Kind == Resume || a.Kind == LimitAlert || a.Kind == Stall {
					t.Fatalf("hard stop got recovery action: %+v", a)
				}
			}
			Apply(w, v, acts)
			if len(w.resumed) != 0 || noteOf(t, w, 2, resetNote("resumed-for", v.Sessions[ref(2)])) {
				t.Fatal("hard stop typed or consumed its resume attempt")
			}
			// Only the executor's explicit restoration permits recovery again.
			w.card(ref(2)).Labels = []string{Running}
			v, err = Gather(w, v.Feature.Ref, taskOnly)
			if err != nil {
				t.Fatal(err)
			}
			Apply(w, v, Decide(v))
			if len(w.resumed) != 1 {
				t.Fatalf("released hard stop did not resume: %v", w.resumed)
			}
		}
	}
}

func TestRecoveryRechecksAHardStopBeforeTyping(t *testing.T) {
	w := limitedWorld(t, false)
	w.now = t0.Add(6 * time.Minute)
	v := gather(t, w)
	acts := Decide(v)
	// The executor enters its hard stop after the pass decided to resume it.
	w.card(ref(2)).Labels = []string{NeedsHuman}
	Apply(w, v, acts)
	if len(w.resumed) != 0 || noteOf(t, w, 2, resetNote("resumed-for", v.Sessions[ref(2)])) {
		t.Fatal("a stale pass typed into a new hard stop")
	}
	if !strings.Contains(strings.Join(w.log, "\n"), "skip automatic resume") {
		t.Fatal("skipped stale action was not explained")
	}
	if strings.Contains(strings.Join(w.log, "\n"), string(Resume)+" "+ref(2).String()+":") {
		t.Fatal("skipped hard stop was logged as a resume")
	}
}

type unreadableResumeTask struct{ World }

func (unreadableResumeTask) Task(tracker.Ref) (tracker.Card, tracker.Ref, error) {
	return tracker.Card{}, tracker.Ref{}, errors.New("tracker unavailable")
}

func TestRecoveryDoesNotTypeWhenTheTaskCannotBeRechecked(t *testing.T) {
	w := limitedWorld(t, false)
	w.now = t0.Add(6 * time.Minute)
	v := gather(t, w)
	Apply(unreadableResumeTask{w}, v, Decide(v))
	if len(w.resumed) != 0 || noteOf(t, w, 2, resetNote("resumed-for", v.Sessions[ref(2)])) {
		t.Fatal("unreadable task typed or consumed its resume attempt")
	}
	if !strings.Contains(strings.Join(w.log, "\n"), "tracker unavailable") {
		t.Fatal("failed recheck was not reported")
	}
}

func TestLimitAlertsLeaveHardStopsAlone(t *testing.T) {
	for _, far := range []bool{false, true} {
		w := limitedWorld(t, false)
		w.card(ref(2)).Labels = []string{NeedsHuman}
		v := gather(t, w)
		s := v.Sessions[ref(2)]
		if far {
			s.ResetsAt = v.Now.Add(25 * time.Hour).Unix()
		} else {
			s.ResumeAt = v.Now.Add(-11 * time.Minute)
		}
		v.Sessions[ref(2)] = s
		if acts := limitActions(v, ref(2)); len(acts) != 0 {
			t.Fatalf("hard stop got limit alert: %+v", acts)
		}
	}
}
