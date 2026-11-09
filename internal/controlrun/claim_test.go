package controlrun

import (
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/reconcile"
)

// The server claims a task as it dispatches it, so a dispatched session
// never waits for its claim. The claim wait and the guard against starting
// a task whose window is live act only once an operator has moved a task
// with a live window out of the held states: failed, then retried.
func TestTheClaimWaitActsOnlyOnARetriedTaskWithALiveWindow(t *testing.T) {
	c, h, _, _, task := setup(t)
	pass(t, c)
	pass(t, c)
	now := c.Service.Now().Add(reconcile.ClaimWait + time.Minute)
	c.Service.Now = func() time.Time { return now }
	pass(t, c)
	if n := claimNotices(t, c, task.ID); n != 0 || h.calls != 1 {
		t.Fatalf("a dispatched task waited for its claim: %d notices, %d launches", n, h.calls)
	}
	for _, state := range []string{"failed", "todo"} {
		st, _ := c.Service.Store.Snapshot()
		execute(t, c.Service, control.Request{Op: "state", ID: task.ID, Version: st.Items[task.ID].Version, State: state})
	}
	pass(t, c)
	pass(t, c)
	if n := claimNotices(t, c, task.ID); n != 1 {
		t.Fatalf("%d claim notices for a retried task with a live window", n)
	}
	if h.calls != 1 {
		t.Fatalf("a task with a live window was dispatched again: %d launches", h.calls)
	}
}

func claimNotices(t *testing.T, c *Coordinator, id string) int {
	t.Helper()
	st, err := c.Service.Store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, u := range st.Updates {
		if u.Item == id && strings.Contains(u.Body, "has not claimed the task") {
			n++
		}
	}
	return n
}
