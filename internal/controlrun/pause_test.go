package controlrun

import (
	"testing"

	"github.com/dinoderek/engine/internal/control"
)

func hostOp(t *testing.T, c *Coordinator, verb string) {
	t.Helper()
	if _, err := c.Service.Execute(control.Actor{Name: "operator", Role: "operator"}, control.Request{Op: "host-" + verb, ID: "devbox", Key: control.Secret()}); err != nil {
		t.Fatal(verb, err)
	}
}

// A paused host gets no dispatch, whatever the passes; resuming it lets the
// next pass dispatch.
func TestAPausedHostGetsNoDispatchUntilResumed(t *testing.T) {
	c, h, _, _, task := setup(t)
	hostOp(t, c, "pause")
	for range 3 {
		pass(t, c)
	}
	st, _ := c.Service.Store.Snapshot()
	if len(st.Attempts) != 0 || h.calls != 0 || st.Items[task.ID].State != "todo" {
		t.Fatalf("a paused host took work: %d attempts, %d launches, task %s", len(st.Attempts), h.calls, st.Items[task.ID].State)
	}
	hostOp(t, c, "resume")
	pass(t, c)
	pass(t, c)
	st, _ = c.Service.Store.Snapshot()
	if h.calls != 1 || st.Items[task.ID].State != "running" {
		t.Fatalf("a resumed host: %d launches, task %s", h.calls, st.Items[task.ID].State)
	}
}

// Pausing a host leaves its running sessions as they are: not ended, not
// released and not dispatched again.
func TestPausingAHostLeavesItsRunningSessionsAlone(t *testing.T) {
	c, h, _, _, task := setup(t)
	pass(t, c)
	pass(t, c)
	claim(t, c, task)
	st, _ := c.Service.Store.Snapshot()
	at := st.Items[task.ID].Attempt
	hostOp(t, c, "pause")
	for range 3 {
		pass(t, c)
	}
	st, _ = c.Service.Store.Snapshot()
	if got := st.Attempts[at]; st.Items[task.ID].Attempt != at || got.State != "running" || got.Reason != "" || h.calls != 1 || len(h.outcomes) != 0 {
		t.Fatalf("pausing touched the session: %+v, %d launches, released %v", got, h.calls, h.outcomes)
	}
}

func TestTheBoardShowsAPausedHost(t *testing.T) {
	c, _, _, _, _ := setup(t)
	board := &HostBoard{}
	c.Board = board
	c.Service.Hosts = board.Status
	hostOp(t, c, "pause")
	pass(t, c)
	if got := operatorRead(t, c.Service); len(got.Hosts) != 1 || !got.Hosts[0].Paused {
		t.Fatalf("fleet %+v", got)
	}
	hostOp(t, c, "resume")
	pass(t, c)
	if got := operatorRead(t, c.Service); got.Hosts[0].Paused {
		t.Fatalf("fleet %+v", got)
	}
}
