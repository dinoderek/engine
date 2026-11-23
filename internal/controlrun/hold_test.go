package controlrun

import (
	"slices"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hostops"
)

func TestStopReleasesTheWindowKeepingItsWorkAndNoPassDispatchesTheHeldTask(t *testing.T) {
	c, h, _, _, task := setup(t)
	pass(t, c)
	pass(t, c)
	claim(t, c, task)
	st, _ := snapshotOf(c.Service.Store)
	first := st.Items[task.ID].Attempt
	launches := h.calls
	stopped := execute(t, c.Service, control.Request{Op: "task-stop", ID: task.ID})
	if stopped.State != "todo" || !stopped.Held {
		t.Fatalf("stop: %+v", stopped)
	}
	// The very next pass already skips the task, window still open or not.
	pass(t, c)
	if h.calls != launches {
		t.Fatalf("a pass dispatched the held task: %d launches, want %d", h.calls, launches)
	}
	if want := []hostops.Outcome{hostops.RunStopped}; !slices.Equal(h.outcomes, want) || !slices.Equal(h.released, []string{"demo-" + task.ID[1:]}) {
		t.Fatalf("released %v with %v, want its window with %v", h.released, h.outcomes, want)
	}
	for range 3 {
		pass(t, c)
	}
	st, _ = snapshotOf(c.Service.Store)
	at := st.Attempts[first]
	if h.calls != launches || st.Items[task.ID].Attempt != first || at.State != "ended" || at.Reason != control.EndStopped || len(h.released) != 1 {
		t.Fatalf("held task: %d launches, attempt %s %+v, %d releases", h.calls, st.Items[task.ID].Attempt, at, len(h.released))
	}
	for _, u := range st.Updates {
		if u.Item == task.ID && u.Recipient == "operator" {
			t.Errorf("a stop raised a notice: %s", u.Body)
		}
	}
}

func TestAStopWhoseReleaseFailedIsRetriedEachPass(t *testing.T) {
	c, h, _, _, task := setup(t)
	pass(t, c)
	pass(t, c)
	execute(t, c.Service, control.Request{Op: "task-stop", ID: task.ID})
	h.outage = true
	pass(t, c)
	if len(h.released) != 0 {
		t.Fatalf("released during an outage: %v", h.released)
	}
	h.outage = false
	pass(t, c)
	if len(h.released) != 1 || h.outcomes[0] != hostops.RunStopped {
		t.Fatalf("released %v with %v", h.released, h.outcomes)
	}
}

func TestResumeDispatchesAgainAsAResumeOfTheKeptBranch(t *testing.T) {
	c, h, _, _, task := setup(t)
	pass(t, c)
	pass(t, c)
	claim(t, c, task)
	st, _ := snapshotOf(c.Service.Store)
	first := st.Items[task.ID].Attempt
	execute(t, c.Service, control.Request{Op: "task-stop", ID: task.ID})
	pass(t, c)
	pass(t, c)
	resumed := execute(t, c.Service, control.Request{Op: "task-resume", ID: task.ID})
	if resumed.Held || resumed.State != "todo" {
		t.Fatalf("resume: %+v", resumed)
	}
	pass(t, c)
	st, _ = snapshotOf(c.Service.Store)
	second := st.Items[task.ID].Attempt
	if second == first || st.Attempts[second].State != "running" || st.Items[task.ID].State != "running" {
		t.Fatalf("the task was not dispatched again: attempt %s, %+v", second, st.Items[task.ID])
	}
	req := h.launches[st.Attempts[second].RunID].Spawn
	if !req.Resume {
		t.Error("the spawn did not ask to resume the kept branch")
	}
	if !strings.Contains(req.Kickoff, "This is a resume") || !strings.Contains(req.Kickoff, "Execution: "+second) {
		t.Errorf("the kickoff does not say it is a resume: %s", req.Kickoff)
	}
	if first := h.launches[st.Attempts[first].RunID].Spawn; first.Resume || strings.Contains(first.Kickoff, "resume") {
		t.Errorf("the first dispatch was a resume: %+v", first)
	}
}
