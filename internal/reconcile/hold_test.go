package reconcile

import (
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
)

// held is a todo task the operator stopped, its attempt ended stopped.
func held(n int, s *Session) Item {
	t := task(n, "todo")
	t.Held, t.Stopped, t.Resume = true, true, true
	t.Current = control.Attempt{ID: "a" + t.ID[1:], Item: t.ID, Host: "devbox", State: "ended", Reason: control.EndStopped}
	t.Attempt = t.Current.ID
	t.Session = s
	return t
}

func TestPlacementSkipsAHeldTaskAndTakesItAfterResume(t *testing.T) {
	stopped := held(2, nil)
	if got := shows(Decide(view(plan("planned"), stopped))); got != "" {
		t.Fatalf("a held task was placed: %q", got)
	}
	// Another ready task is placed meanwhile; the held one still waits.
	if got := shows(Decide(view(plan("planned"), stopped, task(3, "todo")))); got != "dispatch t3 devbox claude false" {
		t.Fatalf("with another task ready: %q", got)
	}
	stopped.Held = false
	if got := shows(Decide(view(plan("planned"), stopped))); got != "dispatch t2 devbox claude true" {
		t.Fatalf("after the resume: %q", got)
	}
}

func TestAStoppedTasksLeftOverWindowIsReleasedKeepingItsWork(t *testing.T) {
	for name, s := range map[string]Session{"live": working(), "dead window": {DeadWindow: true, Spawned: t0.Add(-time.Hour)}} {
		acts := Decide(view(plan("planned"), held(2, &s)))
		if len(acts) != 1 || acts[0].Kind != Release || !acts[0].Stopped || acts[0].Done || acts[0].Attempt != "a2" || acts[0].Item != "t2" {
			t.Errorf("%s: actions %+v", name, acts)
		}
	}
}

func TestAStoppedTaskRaisesNothingOnceItsWindowIsGone(t *testing.T) {
	released := gone()
	if got := shows(Decide(view(plan("planned"), held(2, &released)))); got != "" {
		t.Errorf("a stopped task with no window: %q", got)
	}
	unknown := Session{Unknown: true, Spawned: t0.Add(-time.Hour)}
	if got := shows(Decide(view(plan("planned"), held(2, &unknown)))); got != "" {
		t.Errorf("a stopped task on an unreadable host: %q", got)
	}
	// Not even before the operator resumes it: no "ended before it claimed" stall.
	resumed := held(2, &released)
	resumed.Held = false
	if got := shows(Decide(view(plan("planned"), resumed))); strings.Contains(got, "notice") {
		t.Errorf("a stopped task's ended session was flagged: %q", got)
	}
}

func TestAStoppedSessionIsNotWoken(t *testing.T) {
	s := waiting(time.Minute)
	if held(2, &s).Wakeable() {
		t.Error("a stopped task's session was woken")
	}
}

// A stopped task's window that is still open and limited raises no limit
// notice: the session is being released.
func TestAStoppedTaskRaisesNoLimitStep(t *testing.T) {
	s := working()
	s.Limited, s.ResetsAt = true, t0.Add(48*time.Hour).Unix()
	for _, a := range Decide(view(plan("planned"), held(2, &s))) {
		if a.Kind != Release {
			t.Errorf("a stopped task's limited window raised %s", show(a))
		}
	}
}
