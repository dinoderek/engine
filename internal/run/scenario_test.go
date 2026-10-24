package run

import (
	"slices"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/tracker"
)

// The acceptance scenarios: engine run against a fake world whose executors
// claim, review and merge on a fake clock.

func TestATwoTaskFeatureRunsToDone(t *testing.T) {
	w := newWorld(t, []string{"engine:feature", Approved}, card(2, "standard"), card(3, "deep", 2))
	w.do[2], w.do[3] = smooth, smooth
	Loop(w, ref(1), false, w.sleep)
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
	for _, want := range []string{"label " + ref(1).String() + ": " + Planned, ": " + Executing, ": " + Verifying, "finish"} {
		if !slices.ContainsFunc(w.log, func(l string) bool { return strings.Contains(l, want) }) {
			t.Errorf("log lacks %q:\n%s", want, strings.Join(w.log, "\n"))
		}
	}
}

func TestThePlannerWritesTheTasks(t *testing.T) {
	w := newWorld(t, []string{"engine:feature", Approved})
	w.plans = func(w *world) {
		w.feature.Tasks = append(w.feature.Tasks, card(2, "standard"), card(3, "deep", 2))
	}
	w.do[2], w.do[3] = smooth, smooth
	Loop(w, ref(1), false, w.sleep)
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
	if len(c) != 1 || !strings.Contains(c[0], "has not claimed") || !strings.Contains(c[0], "Do you want to proceed?") || !strings.Contains(c[0], "engine attach r-2") {
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
	Loop(w, ref(2), true, w.sleep)
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
