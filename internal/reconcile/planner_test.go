package reconcile

import (
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
)

// plannerDied is plan p1 whose planner attempt a1 ended, as the server
// recorded it, with its gone window still observed.
func plannerDied(reason string) Item {
	p := with(plan("approved"), gone())
	p.Current.State, p.Current.Reason, p.Current.Ended = "ended", reason, t0.Add(-time.Hour)
	p.Resume = true
	return p
}

// plannerAtLimit is p, whose planner died at a usage limit resetting at
// reset from t0, or, with reset zero, a minute ago at an unknown one.
func plannerAtLimit(p Item, reset time.Duration) Item {
	p.Current.Limited = true
	if reset == 0 {
		p.Current.Ended = t0.Add(-time.Minute)
		return p
	}
	p.Current.ResetsAt = unix(reset)
	return p
}

func TestADeadPlannerIsEndedThenStartedAgain(t *testing.T) {
	for _, tc := range []struct {
		name string
		v    View
		want []string
	}{
		{"a planner whose window is gone without a task is ended", view(with(plan("approved"), gone())), []string{"end p1 a1"}},
		{"a dead planner that wrote tasks leaves the plan to be planned", view(with(plan("approved"), gone()), task(2, "todo")), []string{"state p1 planned"}},
		{"an ended planner without tasks is started again as a resume", view(plannerDied(control.EndEarlyDeath)), []string{"dispatch p1 devbox claude true"}},
		{"a planner ended vanished is started again too", view(plannerDied(control.EndVanished)), []string{"dispatch p1 devbox claude true"}},
		{"a new planner waits for a free local slot", withFleet(view(plannerDied(control.EndEarlyDeath)), fleet(0)), nil},
		{"a new planner waits on the plan's own question", view(question(plannerDied(control.EndEarlyDeath), 4)), nil},
		{"a new planner waits for a known reset", view(plannerAtLimit(plannerDied(control.EndVanished), -time.Minute)), nil},
		{"a new planner starts once the reset has passed", view(plannerAtLimit(plannerDied(control.EndVanished), -ResumeDelay)), []string{"dispatch p1 devbox claude true"}},
		{"with the reset unknown, a new planner waits a probe interval", view(plannerAtLimit(plannerDied(control.EndVanished), 0)), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, want := shows(Decide(tc.v)), strings.Join(tc.want, "\n"); got != want {
				t.Errorf("actions:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

func TestAPlannerProbesAnUnknownResetEveryProbeInterval(t *testing.T) {
	p := plannerAtLimit(plannerDied(control.EndVanished), 0)
	p.Current.Ended = t0.Add(-ProbeEvery)
	if got := shows(Decide(view(p))); got != "dispatch p1 devbox claude true" {
		t.Errorf("actions: %s", got)
	}
}

func TestAPlannerEndCarriesItsTailAndLimit(t *testing.T) {
	s := gone()
	s.PaneTail = "trust this folder?"
	a := Decide(view(with(plan("approved"), s)))[0]
	if a.Text != "the planner demo-1 has died" || a.Tail != "trust this folder?" || a.AtLimit {
		t.Errorf("end: %+v", a)
	}
	s.DiedAtLimit, s.ResetsAt = true, unix(time.Hour)
	a = Decide(view(with(plan("approved"), s)))[0]
	if a.Text != "the planner demo-1 died at a usage limit" || !a.AtLimit || a.ResetsAt != unix(time.Hour) {
		t.Errorf("end at a limit: %+v", a)
	}
}
