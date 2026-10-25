package run

import (
	"errors"
	"os"
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
	v = gather(t, w)
	if acts := Decide(v); slices.ContainsFunc(acts, func(a Action) bool { return a.Kind == StartTask || a.Kind == Stall }) {
		t.Errorf("restarted or flagged a failed task: %+v", acts)
	}
	Apply(w, v, Decide(v))
	if c := w.comments[ref(2)]; len(c) != 1 {
		t.Errorf("comments after the fail %q", c)
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

func TestAReusedWindowIDIsNotKilled(t *testing.T) {
	w := newWorld(t, []string{"engine:feature", Executing}, card(2, "standard"), card(3, "standard"))
	w.do[2], w.do[3] = behaviour{dieAt: 1}, behaviour{claim: 1}
	v := gather(t, w)
	Apply(w, v, Decide(v))
	w.sleep(2 * CheckEvery)
	// The server restarted: 2's window is gone, and its id now names 3's.
	e2, e3 := w.execs[ref(2)], w.execs[ref(3)]
	e3.window.ID, e2.window = e2.window.ID, session.WindowInfo{}
	v = gather(t, w)
	Apply(w, v, Decide(v))
	if slices.Contains(w.killed, e3.window.ID) {
		t.Errorf("killed %v, which is 3's live window", w.killed)
	}
}

func TestAFailedPlannerStartIsReportedOnce(t *testing.T) {
	w := newWorld(t, []string{"engine:feature", Approved})
	w.refuse = func(tracker.Ref) error { return errors.New("no free slot") }
	for range 8 {
		v := gather(t, w)
		Apply(w, v, Decide(v))
		w.now = w.now.Add(PassEvery)
	}
	c := w.comments[ref(1)]
	if len(c) != 1 || !strings.HasPrefix(c[0], "@op ") || !strings.Contains(c[0], "no free slot") {
		t.Errorf("comments %q", c)
	}
	// 40 minutes of passes: a try at 0 and one after the 30-minute rest.
	if w.attempts != 2 {
		t.Errorf("%d planner starts tried, want 2", w.attempts)
	}
}

func TestThePlannerWaitsForAFreeSlot(t *testing.T) {
	w := newWorld(t, []string{"engine:feature", Approved})
	v := gather(t, w)
	v.Fleet.Hosts[0].FreeSlots = 0
	if acts := Decide(v); len(acts) != 0 {
		t.Errorf("no slot: %+v", acts)
	}
	v.Fleet.Hosts[0].FreeSlots = 1
	if acts := Decide(v); len(acts) != 1 || acts[0].Kind != StartPlanner {
		t.Errorf("a slot: %+v", acts)
	}
}

func TestTheSummaryIsPostedOnce(t *testing.T) {
	done := card(2, "standard")
	done.Open = false
	w := newWorld(t, []string{"engine:feature", Executing}, done)
	w.failNext = map[string]error{"comment": errors.New("HTTP 502")}
	for range 2 {
		v := gather(t, w)
		Apply(w, v, Decide(v))
	}
	w.failNext = map[string]error{"close": errors.New("HTTP 502")}
	w.feature.Open = true
	v := gather(t, w)
	Apply(w, v, Decide(v))
	if !w.closed {
		v = gather(t, w)
		Apply(w, v, Decide(v))
	}
	if c := w.comments[ref(1)]; len(c) != 1 || !w.closed {
		t.Errorf("closed %v, comments %q", w.closed, c)
	}
}

func TestAnUnreadableSummaryNoteStopsTheFinish(t *testing.T) {
	done := card(2, "standard")
	done.Open = false
	w := newWorld(t, []string{"engine:feature", Executing}, done)
	dir, _ := w.NoteDir(ref(1))
	if err := mkdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, FinishNoted), []byte("soon"), 0o644); err != nil {
		t.Fatal(err)
	}
	v := gather(t, w)
	Apply(w, v, Decide(v))
	if len(w.comments[ref(1)]) != 0 || w.closed {
		t.Errorf("comments %q, closed %v", w.comments[ref(1)], w.closed)
	}
}

func TestASessionThatDiesBeforeClaimingIsFlaggedOncePerSession(t *testing.T) {
	w := newWorld(t, []string{"engine:feature", Executing}, card(2, "standard"))
	w.do[2] = behaviour{dieAt: 1}
	pass := func() {
		v := gather(t, w)
		Apply(w, v, Decide(v))
	}
	pass()
	w.sleep(2 * CheckEvery)
	pass() // flags the death, closes the dead window, starts again
	pass() // the new session is alive: nothing to flag
	c := w.comments[ref(2)]
	if len(c) != 1 || !strings.HasPrefix(c[0], "@op ") || !strings.Contains(c[0], "ended before it claimed") ||
		!strings.Contains(c[0], "Do you want to proceed?") || strings.Contains(c[0], "engine attach") || len(w.starts) != 2 {
		t.Fatalf("starts %v, comments %q", w.starts, c)
	}
	w.sleep(2 * CheckEvery)
	pass()
	if c := w.comments[ref(2)]; len(c) != 2 || len(w.starts) != 3 {
		t.Errorf("second death: starts %v, comments %q", w.starts, c)
	}
}

func TestTheLoopFlagsASessionThatDiesBeforeClaimingOnceAndRestartsIt(t *testing.T) {
	w := newWorld(t, []string{"engine:feature", Approved}, card(2, "standard"))
	w.do[2] = behaviour{dieAt: 1}
	// The first session dies, the first restart is refused and rests, and the
	// next one runs the task through.
	tries := 0
	w.refuse = func(r tracker.Ref) error {
		tries++
		switch {
		case tries == 2:
			return errors.New("no free slot")
		case tries > 2:
			w.do[r.Number] = smooth
		}
		return nil
	}
	if err := Loop(w, ref(1), false, w.sleep); err != nil {
		t.Fatalf("%v\n%s", err, strings.Join(w.log, "\n"))
	}
	var flags []string
	for _, c := range w.comments[ref(2)] {
		if strings.Contains(c, "ended before it claimed") {
			flags = append(flags, c)
		}
	}
	if len(flags) != 1 || !strings.HasPrefix(flags[0], "@op ") || !strings.Contains(flags[0], "Do you want to proceed?") ||
		strings.Contains(flags[0], "engine attach") {
		t.Errorf("flags %q of comments %q", flags, w.comments[ref(2)])
	}
	if !slices.Equal(w.starts, []tracker.Ref{ref(2), ref(2)}) || tries != 3 || !w.closed || w.card(ref(2)).Open {
		t.Errorf("starts %v, tries %d, closed %v\n%s", w.starts, tries, w.closed, strings.Join(w.log, "\n"))
	}
}
