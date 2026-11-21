package control

// endPlanner sets how at, the current attempt of approved plan, ends once
// its window is confirmed gone. A planner that wrote a task, or died at a
// usage limit, ends vanished. One that wrote none ends early-death, its pane
// log tail kept, for the next pass to start a new planner; the second such
// death since the plan's coordination last started ends failed and stops
// coordination, so a planner that keeps dying never loops. The operator's
// next run starts the count again.
func (s Service) endPlanner(st *State, plan Item, at *Attempt) {
	at.Reason = EndVanished
	if at.Limited || hasTasks(st, plan.ID) {
		return
	}
	at.Early = true
	if plannerDeaths(st, plan, *at) == 0 {
		at.Reason = EndEarlyDeath
		return
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
