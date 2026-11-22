package controlrun

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/remote"
)

// readHosts is a latest pass that read every host named, and none other.
func readHosts(c *Coordinator, names ...string) func() control.HostStatus {
	return func() control.HostStatus {
		out := control.HostStatus{At: c.now()}
		for _, n := range names {
			out.Hosts = append(out.Hosts, control.HostRow{Name: n})
		}
		return out
	}
}

// A move back onto its own host releases the old window, pushing first,
// and only on the next pass, once the window is gone, dispatches the task
// there again as a resume. Nothing it left unpushed is lost, so nobody is
// told.
func TestAMoveReleasesTheOldWindowThenResumesWhereItWasSent(t *testing.T) {
	c, h, _, _, task := setup(t)
	pass(t, c)
	pass(t, c)
	claim(t, c, task)
	st, _ := c.Service.Store.Snapshot()
	first := st.Items[task.ID].Attempt
	launches := h.calls
	c.Service.Hosts = readHosts(c, "devbox")
	h.unpushed = "uncommitted changes in the worktree"
	execute(t, c.Service, control.Request{Op: "task-move", ID: task.ID, Move: &control.MoveRequest{Host: "devbox"}})
	pass(t, c)
	if !slices.Equal(h.outcomes, []hostops.Outcome{hostops.RunMoved}) || h.calls != launches {
		t.Fatalf("first pass after the move: released %v with %v, %d launches, want %d", h.released, h.outcomes, h.calls, launches)
	}
	pass(t, c)
	st, _ = c.Service.Store.Snapshot()
	second := st.Items[task.ID].Attempt
	at := st.Attempts[second]
	if second == first || at.Host != "devbox" || st.Items[task.ID].State != "running" || h.calls != launches+1 {
		t.Fatalf("the moved task was not dispatched again: %+v, %+v", at, st.Items[task.ID])
	}
	req := h.launches[at.RunID].Spawn
	if !req.Resume || !strings.Contains(req.Kickoff, "The operator moved this task here from host devbox.") || strings.Contains(req.Kickoff, "could not be reached") {
		t.Errorf("the resume's kickoff: %s", req.Kickoff)
	}
	if got := operatorNotices(t, c, task.ID); len(got) != 0 {
		t.Errorf("a same-host move told the operator %+v", got)
	}
}

// A moved attempt whose window went by itself is still released, so its
// branch is pushed, and is then never confused with a later session's
// window on the same host, though tmux may give that the same id.
func TestAMovedAttemptNeverClaimsALaterSessionsWindow(t *testing.T) {
	c, h, _, _, task := setup(t)
	pass(t, c)
	pass(t, c)
	claim(t, c, task)
	c.Service.Hosts = readHosts(c, "devbox")
	execute(t, c.Service, control.Request{Op: "task-move", ID: task.ID, Move: &control.MoveRequest{Host: "devbox"}})
	h.status.Windows = nil // the old session exited and its window closed
	pass(t, c)
	if !slices.Equal(h.outcomes, []hostops.Outcome{hostops.RunMoved}) {
		t.Fatalf("the gone window's run was released %v", h.outcomes)
	}
	pass(t, c)
	st, _ := c.Service.Store.Snapshot()
	if at := st.Attempts[st.Items[task.ID].Attempt]; at.Reason == control.EndMoved || at.Window != "@1" {
		t.Fatalf("not dispatched again in a window reusing the id: %+v", at)
	}
	// Only the dispatch's own release of the ended attempt's leftovers ran,
	// before the new window opened.
	released := len(h.released)
	for range 3 {
		pass(t, c)
	}
	if len(h.released) != released || slices.Contains(h.outcomes[1:], hostops.RunMoved) {
		t.Fatalf("the later session's window was released: %v with %v", h.released, h.outcomes)
	}
}

// twoHosts adds host mac beside setup's devbox, each with its own fake.
func twoHosts(c *Coordinator, devbox *host) *host {
	mac := &host{status: hostops.Status{FreeSlots: 1, FreeHeavySlots: 1}, launches: map[string]hostops.ControlRequest{}}
	c.Config.Hosts["mac"] = hosts.Host{Name: "mac", Harnesses: []string{"claude"}, MaxSessions: 2}
	c.Client = func(h hosts.Host) remote.Client {
		if h.Name == "mac" {
			return mac
		}
		return devbox
	}
	return mac
}

// Moving off a host that cannot be read needs the operator's confirmation;
// the task then starts elsewhere at once, its kickoff warning of the old
// session. When the old host answers again, its window is released, pushing
// first, and the operator is told what origin lacks.
func TestABlindMoveStartsElsewhereAndReleasesTheOldWindowOnReturn(t *testing.T) {
	c, devbox, _, _, task := setup(t)
	mac := twoHosts(c, devbox)
	pass(t, c)
	pass(t, c)
	claim(t, c, task)
	st, _ := c.Service.Store.Snapshot()
	first := st.Attempts[st.Items[task.ID].Attempt]
	if first.Host != "devbox" {
		t.Fatalf("dispatched on %s", first.Host)
	}
	devbox.outage = true
	pass(t, c)
	c.Service.Hosts = readHosts(c, "mac")
	_, err := c.Service.Execute(control.Actor{Name: "operator", Role: "operator"}, control.Request{Op: "task-move", ID: task.ID, Key: "move", Move: &control.MoveRequest{}})
	var p *control.Problem
	if !errors.As(err, &p) || p.Reason != control.ReasonMoveUnconfirmed {
		t.Fatalf("an unconfirmed move off an unreachable host: %v", err)
	}
	execute(t, c.Service, control.Request{Op: "task-move", ID: task.ID, Move: &control.MoveRequest{Confirm: true}})
	pass(t, c)
	st, _ = c.Service.Store.Snapshot()
	second := st.Attempts[st.Items[task.ID].Attempt]
	if second.ID == first.ID || second.Host != "mac" || len(mac.launches) != 1 {
		t.Fatalf("the blind move did not start on mac: %+v", second)
	}
	if kickoff := mac.launches[second.RunID].Spawn.Kickoff; !strings.Contains(kickoff, "may still be running and pushing") || !strings.Contains(kickoff, "engine ask") {
		t.Errorf("the blind move's kickoff: %s", kickoff)
	}
	devbox.outage, devbox.unpushed = false, "uncommitted changes in /wt"
	pass(t, c)
	if !slices.Equal(devbox.outcomes, []hostops.Outcome{hostops.RunMoved}) || len(mac.released) != 0 {
		t.Fatalf("on devbox's return: devbox released %v with %v, mac %v", devbox.released, devbox.outcomes, mac.released)
	}
	notices := operatorNotices(t, c, task.ID)
	if len(notices) != 1 || !strings.Contains(notices[0].Body, "Origin lacks uncommitted changes in /wt") || !strings.Contains(notices[0].Body, "host devbox") {
		t.Fatalf("notices %+v", notices)
	}
	pass(t, c)
	if len(devbox.released) != 1 || len(operatorNotices(t, c, task.ID)) != 1 {
		t.Errorf("the gone window was released again: %v", devbox.released)
	}
}

// A blind move off a host that rebooted: when it answers again with no
// window, the old run is still released, pushing its branch, and the
// operator told what origin lacks.
func TestABlindMoveOffARebootedHostStillPushesOnReturn(t *testing.T) {
	c, devbox, _, _, task := setup(t)
	mac := twoHosts(c, devbox)
	pass(t, c)
	pass(t, c)
	claim(t, c, task)
	devbox.outage = true
	pass(t, c)
	c.Service.Hosts = readHosts(c, "mac")
	execute(t, c.Service, control.Request{Op: "task-move", ID: task.ID, Move: &control.MoveRequest{Confirm: true}})
	pass(t, c)
	if len(mac.launches) != 1 {
		t.Fatalf("the blind move did not start on mac: %d launches", len(mac.launches))
	}
	devbox.outage, devbox.status.Windows, devbox.unpushed = false, nil, "2 commits on task/demo-3"
	pass(t, c)
	if !slices.Equal(devbox.outcomes, []hostops.Outcome{hostops.RunMoved}) {
		t.Fatalf("devbox released %v with %v", devbox.released, devbox.outcomes)
	}
	if notices := operatorNotices(t, c, task.ID); len(notices) != 1 || !strings.Contains(notices[0].Body, "Origin lacks 2 commits") {
		t.Fatalf("notices %+v", notices)
	}
}
