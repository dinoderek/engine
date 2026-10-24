package run

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/tracker"
)

// The acceptance scenarios: engine run against a fake world whose executors
// claim, review and merge on a fake clock.

func TestATwoTaskFeatureRunsToDone(t *testing.T) {
	w := newWorld(t, []string{"engine:feature", Approved}, card(2, "standard"), card(3, "deep", 2))
	w.do[2], w.do[3] = smooth, smooth
	if err := Loop(w, ref(1), false, w.sleep); err != nil {
		t.Fatal(err)
	}
	if !w.closed || !w.feature.Has(Done) || w.feature.Has(Executing) || w.feature.Has(Approved) {
		t.Fatalf("feature labels %v, closed %v\n%s", w.feature.Labels, w.closed, strings.Join(w.log, "\n"))
	}
	if !slices.Equal(w.starts, []tracker.Ref{ref(2), ref(3)}) {
		t.Errorf("starts %v", w.starts)
	}
	summary := strings.Join(w.comments[ref(1)], "\n")
	for _, want := range []string{"every task is closed", "pull/102", "pull/103"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary lacks %q:\n%s", want, summary)
		}
	}
	if !slices.Equal(w.history, []string{Planned, Executing, Verifying, Done}) {
		t.Errorf("feature labels went %v", w.history)
	}
}

func TestThePlannerWritesTheTasks(t *testing.T) {
	w := newWorld(t, []string{"engine:feature", Approved})
	w.plans = func(w *world) {
		w.feature.Tasks = append(w.feature.Tasks, card(2, "standard"), card(3, "deep", 2))
	}
	w.do[2], w.do[3] = smooth, smooth
	if err := Loop(w, ref(1), false, w.sleep); err != nil {
		t.Fatal(err)
	}
	if !w.closed || len(w.starts) != 3 || w.starts[0] != ref(1) {
		t.Fatalf("starts %v, closed %v\n%s", w.starts, w.closed, strings.Join(w.log, "\n"))
	}
}

func TestAFailingPlanIsReportedOnce(t *testing.T) {
	bad := card(2, "standard")
	bad.Body = "## Outcome\nx\n"
	w := newWorld(t, []string{"engine:feature", Approved}, bad)
	v := gather(t, w)
	acts := Decide(v)
	if len(acts) != 1 || acts[0].Kind != ReportLint {
		t.Fatalf("actions %+v", acts)
	}
	Apply(w, v, acts)
	if c := w.comments[ref(1)]; len(c) != 1 || !strings.Contains(c[0], "@op") || !strings.Contains(c[0], "no ## Tests section") {
		t.Errorf("comments %q", c)
	}
	if acts := Decide(gather(t, w)); len(acts) != 0 {
		t.Errorf("second pass %+v", acts)
	}
}

func TestStartOrderAndSlots(t *testing.T) {
	// 2 has two tasks downstream, 3 is deep, 4 and 5 are standard.
	w := newWorld(t, []string{"engine:feature", Planned},
		card(2, "light"), card(3, "deep"), card(4, "standard"), card(5, "standard"), card(6, "standard", 2), card(7, "standard", 6))
	w.slots = 3
	acts := Decide(gather(t, w))
	var started []tracker.Ref
	for _, a := range acts {
		if a.Kind == StartTask {
			started = append(started, a.Ref)
		}
	}
	if !slices.Equal(started, []tracker.Ref{ref(2), ref(3), ref(4)}) {
		t.Errorf("started %v from %+v", started, acts)
	}
}

func TestAnUnclaimedSessionStallsOnce(t *testing.T) {
	w := newWorld(t, []string{"engine:feature", Executing}, card(2, "standard"))
	w.do[2] = behaviour{}
	v := gather(t, w)
	Apply(w, v, Decide(v))
	w.sleep(ClaimWait)
	v = gather(t, w)
	Apply(w, v, Decide(v))
	w.sleep(ClaimWait)
	v = gather(t, w)
	Apply(w, v, Decide(v))
	c := w.comments[ref(2)]
	if len(c) != 1 || !strings.HasPrefix(c[0], "@op ") || !strings.Contains(c[0], "has not claimed") || !strings.Contains(c[0], "Do you want to proceed?") || !strings.Contains(c[0], "engine attach r-2") {
		t.Errorf("comments %q", c)
	}
}

func TestAWaitingSessionStallsOncePerWait(t *testing.T) {
	w := newWorld(t, []string{"engine:feature", Executing}, card(2, "standard"))
	w.do[2] = behaviour{claim: 1, waitAt: 2}
	for range 15 {
		v := gather(t, w)
		Apply(w, v, Decide(v))
		w.sleep(CheckEvery)
	}
	c := w.comments[ref(2)]
	if len(c) != 1 || !strings.Contains(c[0], "waited for input") {
		t.Errorf("comments %q", c)
	}
}

func TestADeadSessionFails(t *testing.T) {
	w := newWorld(t, []string{"engine:feature", Executing}, card(2, "standard"), card(3, "standard", 2))
	w.do[2] = behaviour{claim: 1, dieAt: 2}
	v := gather(t, w)
	Apply(w, v, Decide(v))
	w.sleep(3 * CheckEvery)
	_ = w.AddLabels(ref(2), Merging)
	v = gather(t, w)
	Apply(w, v, Decide(v))
	c := w.card(ref(2))
	if !c.Has(Failed) || c.Has(Running) || c.Has(Merging) || len(w.comments[ref(2)]) != 1 {
		t.Errorf("labels %v, comments %q", c.Labels, w.comments[ref(2)])
	}
	if acts := Decide(gather(t, w)); slices.ContainsFunc(acts, func(a Action) bool { return a.Kind == StartTask }) {
		t.Errorf("restarted a failed task: %+v", acts)
	}
}

func TestANeedsHumanTaskBlocksTheFeature(t *testing.T) {
	w := newWorld(t, []string{"engine:feature", Executing}, card(2, "standard"))
	_ = w.AddLabels(ref(2), NeedsHuman)
	acts := Decide(gather(t, w))
	if !slices.ContainsFunc(acts, func(a Action) bool { return a.Kind == SetLabel && a.Label == Blocked }) {
		t.Errorf("actions %+v", acts)
	}
}

func TestOneTaskRunsAlone(t *testing.T) {
	w := newWorld(t, nil, card(2, "standard"))
	w.do[2] = smooth
	if err := Loop(w, ref(2), true, w.sleep); err != nil {
		t.Fatal(err)
	}
	if w.card(ref(2)).Open || len(w.starts) != 1 || w.closed {
		t.Errorf("starts %v, closed feature %v", w.starts, w.closed)
	}
}

func TestAnUnapprovedFeatureExits(t *testing.T) {
	w := newWorld(t, []string{"engine:feature", "engine:prepare"})
	acts := Decide(gather(t, w))
	if len(acts) != 1 || acts[0].Kind != Exit || !strings.Contains(acts[0].Text, "not approved") {
		t.Errorf("actions %+v", acts)
	}
}

func gather(t *testing.T, w *world) View {
	t.Helper()
	v, err := Gather(w, w.feature.Ref, false)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestAFinishedWindowIsReaped(t *testing.T) {
	w := newWorld(t, []string{"engine:feature", Executing}, card(2, "standard"), card(3, "standard", 2))
	w.do[2] = smooth
	v := gather(t, w)
	Apply(w, v, Decide(v))
	w.sleep(5 * CheckEvery)
	e := w.execs[ref(2)]
	e.window, e.status = session.WindowInfo{ID: "@9", Name: "r-2"}, session.AgentIdle
	if err := session.WriteRecord(e.dir, session.Record{RunID: filepath.Base(e.dir), Name: "r-2", Mode: "hands-off", Tmux: "engine:@9", Spawned: e.started}); err != nil {
		t.Fatal(err)
	}
	var reaped bool
	for range 12 {
		v := gather(t, w)
		acts := Decide(v)
		reaped = reaped || slices.ContainsFunc(acts, func(a Action) bool { return a.Kind == Reap && a.Ref == ref(2) })
		Apply(w, v, acts)
		w.sleep(CheckEvery)
	}
	if !reaped || w.execs[ref(2)].window.ID != "" {
		t.Errorf("reaped %v, window %+v", reaped, w.execs[ref(2)].window)
	}
}

func TestAWaitingPlannerIsFlaggedOnce(t *testing.T) {
	w := newWorld(t, []string{"engine:feature", Approved})
	v := gather(t, w)
	Apply(w, v, Decide(v))
	p := w.execs[ref(1)]
	p.status = session.AgentWaiting
	w.plans = nil
	for range 15 {
		v := gather(t, w)
		Apply(w, v, Decide(v))
		w.now = w.now.Add(CheckEvery)
	}
	c := w.comments[ref(1)]
	if len(c) != 1 || !strings.Contains(c[0], "the planner r-1 has waited") || !strings.HasPrefix(c[0], "@op ") {
		t.Errorf("comments %q", c)
	}
}

func TestAPlannerThatWritesNothingIsFlaggedOnce(t *testing.T) {
	w := newWorld(t, []string{"engine:feature", Approved})
	for range 4 {
		v := gather(t, w)
		Apply(w, v, Decide(v))
		w.sleep(CheckEvery)
	}
	c := w.comments[ref(1)]
	if len(w.starts) != 1 || len(c) != 1 || !strings.Contains(c[0], "ended without writing any task") {
		t.Errorf("starts %v, comments %q", w.starts, c)
	}
}

func TestAClosedFeatureEndsTheRun(t *testing.T) {
	w := newWorld(t, []string{"engine:feature", Executing}, card(2, "standard"))
	w.feature.Open = false
	if err := Loop(w, ref(1), false, w.sleep); err != nil || len(w.starts) != 0 {
		t.Errorf("err %v, starts %v", err, w.starts)
	}
}

func TestAnUnapprovedRunFails(t *testing.T) {
	w := newWorld(t, []string{"engine:feature", "engine:prepare"})
	if err := Loop(w, ref(1), false, w.sleep); err == nil || !strings.Contains(err.Error(), "not approved") {
		t.Errorf("err %v", err)
	}
}

func TestAFailedLoneTaskEndsTheRun(t *testing.T) {
	w := newWorld(t, nil, card(2, "standard"))
	_ = w.AddLabels(ref(2), Failed)
	if err := Loop(w, ref(2), true, w.sleep); err == nil || !strings.Contains(err.Error(), Failed) {
		t.Errorf("err %v", err)
	}
}

func TestAFailedStartIsReportedOnceAndHoldsNoSlot(t *testing.T) {
	w := newWorld(t, []string{"engine:feature", Executing}, card(2, "deep"), card(3, "standard"))
	w.slots = 1
	w.refuse = func(r tracker.Ref) error {
		if r == ref(2) {
			return errors.New("refused: bad tier")
		}
		return nil
	}
	w.do[3] = behaviour{claim: 1}
	for range 3 {
		v := gather(t, w)
		Apply(w, v, Decide(v))
		w.now = w.now.Add(CheckEvery)
	}
	c := w.comments[ref(2)]
	if len(c) != 1 || !strings.HasPrefix(c[0], "@op ") || !strings.Contains(c[0], "refused: bad tier") {
		t.Errorf("comments %q", c)
	}
	if !slices.Contains(w.starts, ref(3)) {
		t.Errorf("starts %v: the failed task held the slot", w.starts)
	}
	w.now = w.now.Add(StartRetry)
	w.refuse = nil
	w.slots = 2
	v := gather(t, w)
	if acts := Decide(v); !slices.ContainsFunc(acts, func(a Action) bool { return a.Kind == StartTask && a.Ref == ref(2) }) {
		t.Errorf("no retry after %s: %+v", StartRetry, acts)
	}
}

func TestADeadUnclaimedWindowIsClosedBeforeTheRestart(t *testing.T) {
	w := newWorld(t, []string{"engine:feature", Executing}, card(2, "standard"))
	w.do[2] = behaviour{dieAt: 1}
	v := gather(t, w)
	Apply(w, v, Decide(v))
	w.sleep(2 * CheckEvery)
	w.do[2] = smooth
	v = gather(t, w)
	Apply(w, v, Decide(v))
	if len(w.starts) != 2 {
		t.Errorf("starts %v\n%s", w.starts, strings.Join(w.log, "\n"))
	}
}

func TestACheckTriggersAnEarlyPass(t *testing.T) {
	w := newWorld(t, []string{"engine:feature", Executing}, card(2, "standard"))
	w.do[2] = smooth
	v := gather(t, w)
	Apply(w, v, Decide(v))
	v = gather(t, w)
	if passDue(w, v) {
		t.Error("due with nothing changed")
	}
	w.execs[ref(2)].status = session.AgentWaiting
	if !passDue(w, v) {
		t.Error("not due after the session began to wait")
	}
}
