package run

import (
	"slices"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/tracker"
)

// The scenarios of a run whose task is a Codex session. Its window's state
// comes from the harness's state file, not from Claude Code's list, so every
// scenario runs with that list down: nothing here may read it.

// codexWorld is a feature of one Codex task, 2, on a host that runs both
// harnesses and whose Claude Code list fails.
func codexWorld(t *testing.T, do behaviour) *world {
	t.Helper()
	w := newWorld(t, []string{"engine:feature", Executing}, onCodex(card(2, "standard")))
	w.hosts = []fakeHost{{name: "mac", slots: 3, caps: []string{"macos"}, harnesses: []string{"claude", "codex"}, agentsDown: "claude agents --json: exit status 1"}}
	w.do[2] = do
	return w
}

func TestACodexSessionThatWaitsStallsOncePerWait(t *testing.T) {
	w := codexWorld(t, behaviour{claim: 1, waitAt: 2})
	for range 15 {
		v := gather(t, w)
		Apply(w, v, Decide(v))
		w.sleep(CheckEvery)
	}
	c := w.comments[ref(2)]
	if len(c) != 1 || !strings.HasPrefix(c[0], "@op ") || !strings.Contains(c[0], "waited for input") || !strings.Contains(c[0], "outside a hard stop") {
		t.Errorf("comments %q", c)
	}
}

// A Codex session at a hard stop waits for the operator, and is not flagged.
func TestACodexSessionAtAHardStopIsNotFlagged(t *testing.T) {
	w := codexWorld(t, behaviour{claim: 1, waitAt: 2})
	for i := range 15 {
		v := gather(t, w)
		Apply(w, v, Decide(v))
		if i == 2 {
			_ = w.RemoveLabel(ref(2), Running)
			_ = w.AddLabels(ref(2), NeedsHuman)
		}
		w.sleep(CheckEvery)
	}
	if c := w.comments[ref(2)]; len(c) != 0 {
		t.Errorf("comments %q", c)
	}
}

func TestACodexSessionThatDiesBeforeClaimingIsFlaggedOnceAndRestarted(t *testing.T) {
	w := codexWorld(t, behaviour{dieAt: 1})
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
		!strings.Contains(c[0], "Do you want to proceed?") || !slices.Equal(w.starts, []tracker.Ref{ref(2), ref(2)}) {
		t.Errorf("starts %v, comments %q", w.starts, c)
	}
}

func TestACodexSessionThatStaysSilentBeforeClaimingStallsOnce(t *testing.T) {
	w := codexWorld(t, behaviour{})
	v := gather(t, w)
	Apply(w, v, Decide(v))
	// It sits at a prompt, so its state file says waiting and its host sends
	// its screen.
	w.execs[ref(2)].status = session.AgentWaiting
	for range 2 {
		w.sleep(ClaimWait)
		v = gather(t, w)
		Apply(w, v, Decide(v))
	}
	c := w.comments[ref(2)]
	if len(c) != 1 || !strings.HasPrefix(c[0], "@op ") || !strings.Contains(c[0], "has not claimed") || !strings.Contains(c[0], "Do you want to proceed?") {
		t.Errorf("comments %q", c)
	}
}

// A finished hands-off Codex window that went quiet is reaped, at once.
func TestDoneReapsAFinishedCodexWindow(t *testing.T) {
	w := codexWorld(t, behaviour{claim: 1, review: 3, merge: 4, linger: true, waitAt: 5})
	w.feature.Labels = []string{"engine:feature", Approved}
	if err := Loop(w, ref(1), false, w.sleep); err != nil {
		t.Fatal(err)
	}
	if !w.closed || len(w.killed) != 1 || w.execs[ref(2)].window.ID != "" {
		t.Errorf("closed %v, killed %v, window %+v\n%s", w.closed, w.killed, w.execs[ref(2)].window, strings.Join(w.log, "\n"))
	}
}
