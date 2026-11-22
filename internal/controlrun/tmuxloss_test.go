package controlrun

import (
	"fmt"
	"testing"

	"github.com/dinoderek/engine/internal/control"
)

// hostNotices are the operator notices about no item: the host's.
func hostNotices(c *Coordinator) []string {
	st, _ := c.Service.Store.Snapshot()
	var out []string
	for _, u := range st.Updates {
		if u.Item == "" && u.Recipient == "operator" {
			out = append(out, u.Body)
		}
	}
	return out
}

// twoRunning is the setup's task, claimed, and a second one that never
// claims, both running on devbox and seen live.
func twoRunning(t *testing.T) (*Coordinator, *host, control.Item, control.Item) {
	t.Helper()
	c, h, repo, plan, claimed := setup(t)
	unclaimed := execute(t, c.Service, control.Request{Op: "create", Item: control.Item{Kind: "task", Title: "Other", Repo: repo.ID, Plan: plan.ID, Body: claimed.Body}})
	execute(t, c.Service, control.Request{Op: "create", Item: control.Item{Kind: "task", Title: "Acceptance", Repo: repo.ID, Plan: plan.ID, Body: claimed.Body, Depends: []string{claimed.ID, unclaimed.ID}}})
	h.status.FreeSlots = 2
	for range 3 {
		pass(t, c)
	}
	claim(t, c, claimed)
	pass(t, c)
	st, _ := c.Service.Store.Snapshot()
	for _, id := range []string{claimed.ID, unclaimed.ID} {
		if at := st.Attempts[st.Items[id].Attempt]; at.State != "running" || at.LiveAt.IsZero() {
			t.Fatalf("%s is not running and seen live: %+v", id, at)
		}
	}
	return c, h, claimed, unclaimed
}

func TestALostTmuxServerEndsEveryAttemptAsOneEventWithOneNotice(t *testing.T) {
	c, h, claimed, unclaimed := twoRunning(t)
	before, _ := c.Service.Store.Snapshot()
	a1, a2 := before.Items[claimed.ID].Attempt, before.Items[unclaimed.ID].Attempt
	h.status.Windows, h.status.TmuxStart = nil, 2000
	pass(t, c)
	st, _ := c.Service.Store.Snapshot()
	e1, e2 := st.Attempts[a1], st.Attempts[a2]
	if e1.Reason != control.EndVanished || e1.Crashed || !e1.HostLost || st.Items[claimed.ID].State != "running" {
		t.Errorf("the claimed attempt: %+v, task %s", e1, st.Items[claimed.ID].State)
	}
	if e2.Reason != control.EndEarlyDeath || e2.Early || !e2.HostLost || st.Items[unclaimed.ID].State != "todo" {
		t.Errorf("the unclaimed attempt: %+v, task %s", e2, st.Items[unclaimed.ID].State)
	}
	if got := hostNotices(c); len(got) != 1 || got[0] != "devbox's tmux restarted; 2 tasks will resume" {
		t.Fatalf("host notices: %q", got)
	}
	if got := taskNotices(c, claimed); len(got) != 0 {
		t.Errorf("the claimed task got a notice of its own: %q", got)
	}
	// Later passes read the same start time: no second notice, and the tasks
	// start again.
	h.status.FreeSlots = 2
	pass(t, c)
	pass(t, c)
	st, _ = c.Service.Store.Snapshot()
	if got := hostNotices(c); len(got) != 1 {
		t.Fatalf("a later pass sent a second host notice: %q", got)
	}
	if st.Items[claimed.ID].Attempt == a1 || st.Items[unclaimed.ID].Attempt == a2 {
		t.Errorf("a task was not dispatched again: %+v", st.Items)
	}
}

func TestOneWindowGoneWithTheTmuxStartUnchangedFollowsThePerTaskRule(t *testing.T) {
	c, h, claimed, unclaimed := twoRunning(t)
	st, _ := c.Service.Store.Snapshot()
	a1, a2 := st.Items[claimed.ID].Attempt, st.Items[unclaimed.ID].Attempt
	for i, w := range h.status.Windows {
		if w.Name == fmt.Sprintf("demo-%d", control.Number(claimed.ID)) {
			h.status.Windows[i].Alive = false
		}
	}
	pass(t, c)
	st, _ = c.Service.Store.Snapshot()
	if e := st.Attempts[a1]; e.State != "ended" || e.HostLost || !e.Crashed {
		t.Errorf("a single death was read as a host-wide loss: %+v", e)
	}
	if e := st.Attempts[a2]; e.State == "ended" || e.HostLost {
		t.Errorf("the other session was ended: %+v", e)
	}
	if got := hostNotices(c); len(got) != 0 {
		t.Errorf("host notices: %q", got)
	}
}
