package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/session"
)

// The scenarios of a run that has reached done: what it does with the
// windows its executors left open.

// finishedFeature is a feature whose one task has merged but whose executor
// keeps its window open, as the given behaviour says.
func finishedFeature(t *testing.T, do behaviour) *world {
	t.Helper()
	w := newWorld(t, []string{"engine:feature", Approved}, card(2, "standard"))
	do.claim, do.review, do.merge, do.linger = 1, 3, 4, true
	w.do[2] = do
	return w
}

func TestDoneReapsAWaitingWindowAtOnce(t *testing.T) {
	w := finishedFeature(t, behaviour{waitAt: 5})
	if err := Loop(w, ref(1), false, w.sleep); err != nil {
		t.Fatal(err)
	}
	if !w.closed || len(w.killed) != 1 {
		t.Errorf("closed %v, killed %v\n%s", w.closed, w.killed, strings.Join(w.log, "\n"))
	}
	if got := w.now.Sub(t0); got > 12*time.Minute {
		t.Errorf("the run lasted %s: the reap waited for the stall time", got)
	}
}

func TestDoneWaitsForABusyWindowThenReapsIt(t *testing.T) {
	w := finishedFeature(t, behaviour{waitAt: 9})
	if err := Loop(w, ref(1), false, w.sleep); err != nil {
		t.Fatal(err)
	}
	if len(w.killed) != 1 || w.execs[ref(2)].window.ID != "" {
		t.Errorf("killed %v, window %+v", w.killed, w.execs[ref(2)].window)
	}
	if got := w.now.Sub(t0); got > 16*time.Minute {
		t.Errorf("the run lasted %s: the reap did not follow the wait", got)
	}
}

func TestDoneKeepsAWindowWhoseTaskStillHoldsALabel(t *testing.T) {
	w := finishedFeature(t, behaviour{waitAt: 5})
	for range 2 {
		v := gather(t, w)
		Apply(w, v, Decide(v))
		w.sleep(PassEvery)
	}
	w.sleep(PassEvery)
	c := w.card(ref(2))
	if c.Open {
		t.Fatalf("the task has not merged:\n%s", strings.Join(w.log, "\n"))
	}
	c.Labels = []string{InReview}
	acts, live := doneReaps(gather(t, w))
	if len(acts) != 0 || live != 1 {
		t.Errorf("reaps %v, live %d", acts, live)
	}
	c.Labels = nil
	if acts, live = doneReaps(gather(t, w)); len(acts) != 1 || live != 0 {
		t.Errorf("a finished task: reaps %v, live %d", acts, live)
	}
}

func TestDoneGivesUpOnAWindowThatStaysBusy(t *testing.T) {
	w := finishedFeature(t, behaviour{})
	if err := Loop(w, ref(1), false, w.sleep); err != nil {
		t.Fatal(err)
	}
	if len(w.killed) != 0 || w.execs[ref(2)].window.ID == "" {
		t.Errorf("a busy window was closed: killed %v", w.killed)
	}
	if got := w.now.Sub(t0); got < DoneWait+5*time.Minute || got > DoneWait+12*time.Minute {
		t.Errorf("the run lasted %s after starting; want the work plus DoneWait", got)
	}
	if !strings.Contains(strings.Join(w.log, "\n"), "wind down:") {
		t.Errorf("no note that a window was left:\n%s", strings.Join(w.log, "\n"))
	}
}

func TestDoneLeavesAGatedWindowAlone(t *testing.T) {
	w := finishedFeature(t, behaviour{waitAt: 5, gated: true})
	if err := Loop(w, ref(1), false, w.sleep); err != nil {
		t.Fatal(err)
	}
	if len(w.killed) != 0 || w.execs[ref(2)].window.ID == "" {
		t.Errorf("a gated window was closed: killed %v", w.killed)
	}
	if got := w.now.Sub(t0); got > 11*time.Minute {
		t.Errorf("the run waited %s for a gated window", got)
	}
}

func TestAFailedRunDoesNotWindDown(t *testing.T) {
	w := newWorld(t, []string{"engine:feature"}, card(2, "standard"))
	w.execs[ref(2)] = &executor{window: session.WindowInfo{ID: "@1", Name: "r-2"}}
	if err := Loop(w, ref(1), false, w.sleep); err == nil || len(w.killed) != 0 || !w.now.Equal(t0) {
		t.Errorf("err %v, killed %v, now %s", err, w.killed, w.now)
	}
}

func TestAPassThatReadsTasksMakesNoDirectory(t *testing.T) {
	w := newWorld(t, []string{"engine:feature", Executing}, card(2, "standard"), card(3, "standard", 2))
	w.do[2] = smooth
	for range 3 {
		v := gather(t, w)
		Apply(w, v, Decide(v))
		w.sleep(CheckEvery)
	}
	// Only the start of 2 is noted: reading 3, and 2's session working, make
	// no directory.
	entries, err := os.ReadDir(filepath.Join(w.home, "notes"))
	if err != nil || len(entries) != 1 || entries[0].Name() != "2" {
		t.Fatalf("note directories %v, %v", entries, err)
	}
	notes, err := os.ReadDir(filepath.Join(w.home, "notes", "2"))
	if err != nil || len(notes) != 1 || notes[0].Name() != StartedOn+"mac" {
		t.Errorf("notes of 2: %v, %v", notes, err)
	}
}
