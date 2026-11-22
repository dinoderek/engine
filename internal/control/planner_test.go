package control

import (
	"fmt"
	"testing"
	"time"
)

// ticking moves s's clock on a minute at each read, as passes do.
func ticking(s Service) Service {
	now := s.now()
	s.Now = func() time.Time { now = now.Add(time.Minute); return now }
	return s
}

// coordinatedPlan is an approved plan with coordination running and no task.
func coordinatedPlan(t *testing.T, s Service) (Item, Item) {
	t.Helper()
	repo, plan := repoPlan(t, s)
	plan = approve(t, s, plan)
	return repo, *call(t, s, op, Request{Op: "run", ID: plan.ID, Version: plan.Version}).Item
}

// plannerDies dispatches plan's planner, sees its window live past the
// claim wait, and ends it dead with tail.
func plannerDies(t *testing.T, s Service, plan Item, tail string) (Attempt, Item) {
	t.Helper()
	current := *call(t, s, op, Request{Op: "get", ID: plan.ID}).Item
	_, out := dispatch(t, s, current)
	launched(t, s, out.Attempt.ID)
	for range ClaimWait / time.Minute {
		seenLive(t, s, out.Attempt.ID)
	}
	at := dies(t, s, out.Attempt.ID, tail)
	return at, *call(t, s, op, Request{Op: "get", ID: plan.ID}).Item
}

func TestAPlannerThatWroteNoTaskIsStartedAgainOnceThenCoordinationStops(t *testing.T) {
	s := ticking(service(t))
	_, plan := coordinatedPlan(t, s)
	at, plan := plannerDies(t, s, plan, "trust this folder?")
	if at.Reason != EndEarlyDeath || !at.Early || at.Tail != "trust this folder?" || !plan.Coordinating {
		t.Fatalf("the first death: %+v, coordinating %v", at, plan.Coordinating)
	}
	at, plan = plannerDies(t, s, plan, "again")
	if at.Reason != EndFailed || !at.Early || at.Tail != "again" || plan.Coordinating || plan.StoppedAt.IsZero() {
		t.Fatalf("the second death: %+v, coordinating %v stopped %v", at, plan.Coordinating, plan.StoppedAt)
	}
	plan = *call(t, s, op, Request{Op: "run", ID: plan.ID, Version: plan.Version}).Item
	if at, _ = plannerDies(t, s, plan, "after a run"); at.Reason != EndEarlyDeath {
		t.Fatalf("a run did not start the count again: %+v", at)
	}
}

func TestAPlannerThatWroteATaskOrDiedAtALimitEndsVanished(t *testing.T) {
	s := ticking(service(t))
	_, plan := coordinatedPlan(t, s)
	current := *call(t, s, op, Request{Op: "get", ID: plan.ID}).Item
	_, out := dispatch(t, s, current)
	launched(t, s, out.Attempt.ID)
	at, err := s.EndAtLimit(out.Attempt.ID, "died at a usage limit", 0)
	if err != nil || at.Reason != EndVanished || at.Early || !at.Limited {
		t.Fatalf("a death at a limit: %+v %v", at, err)
	}
	if at, _ = plannerDies(t, s, plan, "first"); at.Reason != EndEarlyDeath {
		t.Fatalf("a death at a limit counted toward the bound: %+v", at)
	}
	newTask(t, s, plan.Repo, plan.ID)
	if at, _ = plannerDies(t, s, plan, "wrote a task"); at.Reason != EndVanished || at.Early || !at.Crashed || at.Tail != "wrote a task" {
		t.Fatalf("a planner that wrote a task: %+v", at)
	}
}

func TestAPlannerThatWroteTasksResumesUntilTheCapThenCoordinationStops(t *testing.T) {
	s := ticking(service(t))
	_, plan := coordinatedPlan(t, s)
	newTask(t, s, plan.Repo, plan.ID)
	for i := range DeathCap - 1 {
		at, current := plannerDies(t, s, plan, fmt.Sprint("crash ", i))
		if at.Reason != EndVanished || !at.Crashed || !current.Coordinating {
			t.Fatalf("death %d: %+v, coordinating %v", i, at, current.Coordinating)
		}
	}
	at, current := plannerDies(t, s, plan, "the cap")
	if at.Reason != EndFailed || !at.Crashed || at.Early || at.Tail != "the cap" || current.Coordinating || current.StoppedAt.IsZero() {
		t.Fatalf("the cap's death: %+v, coordinating %v stopped %v", at, current.Coordinating, current.StoppedAt)
	}
	current = *call(t, s, op, Request{Op: "run", ID: plan.ID, Version: current.Version}).Item
	if at, _ = plannerDies(t, s, current, "after a run"); at.Reason != EndVanished {
		t.Fatalf("a run did not start the count again: %+v", at)
	}
}

func TestAPlannerThatWroteTasksAndDiedWhileStoppedIsUncounted(t *testing.T) {
	s := ticking(service(t))
	_, plan := coordinatedPlan(t, s)
	newTask(t, s, plan.Repo, plan.ID)
	for range DeathCap - 1 {
		plannerDies(t, s, plan, "crash")
	}
	current := *call(t, s, op, Request{Op: "get", ID: plan.ID}).Item
	_, out := dispatch(t, s, current)
	launched(t, s, out.Attempt.ID)
	seenLive(t, s, out.Attempt.ID)
	call(t, s, op, Request{Op: "stop", ID: plan.ID})
	at := dies(t, s, out.Attempt.ID, "stopped")
	if at.Reason != EndVanished || at.Crashed {
		t.Fatalf("a death after coordination stopped counted: %+v", at)
	}
}

// A planner lost in a host-wide loss counts toward neither the early-death
// strikes nor the crash cap, with or without a task.
func TestAPlannerLostInAHostWideLossCountsNothing(t *testing.T) {
	s := ticking(service(t))
	_, plan := coordinatedPlan(t, s)
	lose := func() Attempt {
		current := *call(t, s, op, Request{Op: "get", ID: plan.ID}).Item
		_, out := dispatch(t, s, current)
		launched(t, s, out.Attempt.ID)
		seenLive(t, s, out.Attempt.ID)
		if n, err := s.SawTmux("devbox", TmuxRead{}, windows()); err != nil || n != 1 {
			t.Fatalf("the loss marked %d, %v", n, err)
		}
		at, err := s.EndDead(out.Attempt.ID, "gone", "tail")
		if err != nil {
			t.Fatal(err)
		}
		return at
	}
	for i := range DeathCap + 1 {
		if at := lose(); at.Reason != EndEarlyDeath || at.Early || !at.HostLost || at.Tail != "tail" {
			t.Fatalf("loss %d of a planner with no task: %+v", i+1, at)
		}
	}
	current := *call(t, s, op, Request{Op: "get", ID: plan.ID}).Item
	if !current.Coordinating {
		t.Fatal("host-wide losses stopped coordination")
	}
	newTask(t, s, plan.Repo, plan.ID)
	for i := range DeathCap + 1 {
		if at := lose(); at.Reason != EndVanished || at.Crashed {
			t.Fatalf("loss %d of a planner that wrote a task: %+v", i+1, at)
		}
	}
}
