package controlrun

import (
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/session"
)

// plannerSetup is setup with its plan's task removed, so the plan's first
// pass starts a planner, and a clock that moves on a minute at each read.
func plannerSetup(t *testing.T) (*Coordinator, *host, control.Item) {
	t.Helper()
	c, h, _, plan, task := setup(t)
	if err := c.Service.Store.Transaction(func(st *control.State) error { delete(st.Items, task.ID); return nil }); err != nil {
		t.Fatal(err)
	}
	now := c.Service.Now()
	c.Service.Now = func() time.Time { now = now.Add(time.Minute); return now }
	return c, h, plan
}

// plannerGone runs the pass that starts a planner, holds it at a dialog,
// then takes its window away, as a stopped tmux server does, freeing its
// slot and leaving its run record with tail. It returns the planner's attempt after the next pass.
func plannerGone(t *testing.T, c *Coordinator, h *host, plan control.Item, tail string) control.Attempt {
	t.Helper()
	pass(t, c)
	st, _ := c.Service.Store.Snapshot()
	at := st.Attempts[st.Items[plan.ID].Attempt]
	if at.State == "ended" || len(h.status.Windows) != 1 {
		t.Fatalf("no planner was started: %+v, windows %v", at, h.status.Windows)
	}
	h.status.Windows[0].State, h.status.Windows[0].Dialog = session.StateWaiting, true
	pass(t, c)
	h.status.Windows, h.status.FreeSlots = nil, 2
	for i, r := range h.status.Runs {
		if r.Record.RunID == at.RunID {
			h.status.Runs[i].PaneTail = tail
		}
	}
	pass(t, c)
	st, _ = c.Service.Store.Snapshot()
	return st.Attempts[at.ID]
}

// A planner whose window goes before it writes a task is ended and a new
// planner, a resume, takes its place; a second such death stops the plan's
// coordination with both tails, and the operator's run starts a planner again.
func TestAPlannerThatDiesWithoutATaskIsReplacedThenStops(t *testing.T) {
	c, h, plan := plannerSetup(t)
	first := plannerGone(t, c, h, plan, "Do you trust the files in this folder?")
	if first.Reason != control.EndEarlyDeath {
		t.Fatalf("the first planner's end: %+v", first)
	}
	if n := taskNotices(c, plan); len(n) != 1 || !strings.Contains(n[0], "without writing any task; a new planner is started") ||
		!strings.Contains(n[0], "Do you trust the files") {
		t.Fatalf("the first death's notice: %q", n)
	}
	second := plannerGone(t, c, h, plan, "claude: command not found")
	st, _ := c.Service.Store.Snapshot()
	if second.ID == first.ID || second.Reason != control.EndFailed || st.Items[plan.ID].Coordinating {
		t.Fatalf("the second planner's end: %+v, coordinating %v", second, st.Items[plan.ID].Coordinating)
	}
	for _, req := range h.launches {
		if req.Spawn.RunID == second.RunID && !req.Spawn.Resume {
			t.Fatal("the new planner was not a resume")
		}
	}
	n := taskNotices(c, plan)
	if last := n[len(n)-1]; !strings.Contains(last, "for the second time; coordination of the plan stopped") ||
		!strings.Contains(last, "Do you trust the files") || !strings.Contains(last, "claude: command not found") {
		t.Fatalf("the second death's notice: %q", last)
	}
	pass(t, c)
	if st, _ = c.Service.Store.Snapshot(); len(st.Attempts) != 2 {
		t.Fatalf("a stopped plan started a planner: %d attempts", len(st.Attempts))
	}
	execute(t, c.Service, control.Request{Op: "run", ID: plan.ID, Version: st.Items[plan.ID].Version})
	if third := plannerGone(t, c, h, plan, "once more"); third.Reason != control.EndEarlyDeath {
		t.Fatalf("a run did not start the count again: %+v", third)
	}
}

// A planner that died at a usage limit is started again once the reset has
// passed, and the notice says when.
func TestAPlannerDeadAtALimitIsStartedAfterTheReset(t *testing.T) {
	c, h, plan := plannerSetup(t)
	pass(t, c)
	st, _ := c.Service.Store.Snapshot()
	at := st.Attempts[st.Items[plan.ID].Attempt]
	reset := c.Service.Now().Add(time.Hour)
	h.status.Windows, h.status.FreeSlots = nil, 2
	h.status.Runs[0].Limit = &hostops.RunLimit{ResetsAt: reset.Unix()}
	pass(t, c)
	st, _ = c.Service.Store.Snapshot()
	if ended := st.Attempts[at.ID]; ended.Reason != control.EndVanished || !ended.Limited {
		t.Fatalf("the planner's end: %+v", ended)
	}
	if n := taskNotices(c, plan); len(n) != 1 || !strings.Contains(n[0], "died at a usage limit; a new planner is started at "+reset.Add(2*time.Minute).UTC().Format(time.RFC3339)) {
		t.Fatalf("notices %q", n)
	}
	pass(t, c)
	if st, _ = c.Service.Store.Snapshot(); len(st.Attempts) != 1 {
		t.Fatal("a planner started before the reset")
	}
	later := reset.Add(3 * time.Minute)
	c.Service.Now = func() time.Time { return later }
	pass(t, c)
	if st, _ = c.Service.Store.Snapshot(); len(st.Attempts) != 2 {
		t.Fatal("no planner started after the reset")
	}
}
