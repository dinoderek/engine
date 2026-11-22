package control

// endPlanner sets how at, the current attempt of approved plan, ends once
// its window is confirmed gone. A planner that died at a usage limit ends
// vanished. One that wrote tasks ends vanished too, a crash unless
// coordination stopped after it was last seen live, until DeathCap crashes
// in a row since coordination last started; that one ends failed. One that
// wrote none ends early-death, its pane log tail kept, for the next pass to
// start a new planner; the second such death since the plan's coordination
// last started ends failed. A planner lost in a host-wide loss (at.HostLost)
// ends vanished, or early-death with Early unset when it wrote no task:
// neither counts. A failed planner stops coordination, so a
// planner that keeps dying never loops; the operator's next run starts the
// counts again.
func (s Service) endPlanner(st *State, plan Item, at *Attempt) {
	at.Reason = EndVanished
	switch {
	case at.Limited:
		return
	case at.HostLost:
		if !hasTasks(st, plan.ID) {
			at.Reason = EndEarlyDeath
		}
		return
	case hasTasks(st, plan.ID):
		if stoppedSince(st, plan, *at) {
			return
		}
		at.Crashed = true
		if claimedDeaths(st, *at, plan.StoppedAt) < DeathCap-1 {
			return
		}
	default:
		at.Early = true
		if plannerDeaths(st, plan, *at) == 0 {
			at.Reason = EndEarlyDeath
			return
		}
	}
	at.Reason = EndFailed
	if plan.Coordinating {
		plan.Coordinating, plan.StoppedAt = false, s.now()
		plan.Version++
		st.Items[plan.ID] = plan
	}
}

// hasTasks reports whether plan id has a task.
func hasTasks(st *State, id string) bool {
	for _, t := range st.Items {
		if t.Kind == "task" && t.Plan == id {
			return true
		}
	}
	return false
}

// plannerDeaths counts plan's other planner attempts that died without
// writing a task and started after its coordination last stopped.
func plannerDeaths(st *State, plan Item, at Attempt) int {
	n := 0
	for _, o := range st.Attempts {
		if o.Item == plan.ID && o.ID != at.ID && o.Early && !o.Started.Before(plan.StoppedAt) {
			n++
		}
	}
	return n
}
