package reconcile

import "testing"

// pausedHosts is devbox and mac with free slots, devbox paused.
func pausedHosts() Fleet {
	f := moveHosts()
	f.Hosts[0].Paused = true
	return f
}

func TestAPausedHostGetsNoDispatchAndAResumedHostDoes(t *testing.T) {
	v := view(plan("planned"), task(2, "todo"))
	v.Fleet = pausedHosts()
	// devbox has the most free slots, so only its pause sends the task to mac.
	if got := shows(Decide(v)); got != "dispatch t2 mac claude false" {
		t.Errorf("devbox paused: %q", got)
	}
	v.Fleet.Hosts[0].Paused = false
	if got := shows(Decide(v)); got != "dispatch t2 devbox claude false" {
		t.Errorf("devbox resumed: %q", got)
	}
}

func TestATaskWaitsQuietlyWhileEveryHostItCouldUseIsPaused(t *testing.T) {
	v := view(plan("planned"), task(2, "todo"))
	v.Fleet = pausedHosts()
	v.Fleet.Hosts[1].Paused = true
	if got := shows(Decide(v)); got != "" {
		t.Errorf("every host paused: %q: a pause is no reason to tell the operator", got)
	}
	p := v.Fleet.Place([]Want{{ID: "t2", Repo: "r1"}})
	if len(p) != 1 || p[0].Host != "" || p[0].Notify || p[0].Wait == "" {
		t.Errorf("placement %+v", p)
	}
}

func TestAMovedTaskDoesNotResumeOnAPausedHostItWasSentTo(t *testing.T) {
	v := moveView(moved(2, "running", "devbox", ""))
	v.Fleet = pausedHosts()
	if got := shows(Decide(v)); got != "" {
		t.Errorf("moved to a paused host: %q", got)
	}
}

func TestAPausedHostStartsNoPlanner(t *testing.T) {
	v := view(plan("approved"))
	if got := shows(Decide(v)); got != "dispatch p1 devbox claude false" {
		t.Fatalf("an unpaused host: %q", got)
	}
	v.Fleet.Hosts[0].Paused = true
	if got := shows(Decide(v)); got != "" {
		t.Errorf("a paused host started a planner: %q", got)
	}
}

func TestPausingLeavesItsRunningSessionsAlone(t *testing.T) {
	running := with(task(2, "running"), working())
	v := view(plan("planned"), running)
	v.Fleet.Hosts[0].Paused = true
	if got := shows(Decide(v)); got != "" {
		t.Errorf("a paused host's running task: %q", got)
	}
}
