package ps

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/tracker"
)

// A claim on another host is settled from its status as one here is: a
// window working or waiting, of either harness, and a window that is gone, or
// whose command exited, is dead.
func TestRemoteRowsAreSettledFromStatus(t *testing.T) {
	waiting := win("w-2", session.StateWaiting)
	waiting.WaitingFor = "approval"
	codex := hostops.Window{Name: "w-6", ID: "@6", Alive: true, Harness: hostops.Codex, State: session.StateWaiting}
	f := &fake{
		tasks: []tracker.ClaimedTask{
			onHost(task(1, "w-1", "engine:running"), "devbox"),
			onHost(task(2, "w-2", "engine:running"), "devbox"),
			onHost(task(3, "w-3", "engine:running"), "devbox"),
			onHost(task(4, "w-4", "engine:running"), "devbox"),
			onHost(task(5, "w-5", "engine:in-review"), "devbox"),
			onHost(task(6, "w-6", "engine:running"), "devbox"),
		},
		statuses: map[string]hostops.Status{"devbox": {
			Windows: []hostops.Window{win("w-1", session.StateWorking), waiting, deadWin("w-4"), win("w-5", session.StateWorking), codex},
			Agents:  []session.Agent{agent("w-3", session.AgentBusy)},
		}},
	}
	l := list(t, f)
	type want struct{ state, alive, waitingFor string }
	wants := map[int]want{
		1: {Working, "alive", ""},
		2: {Waiting, "alive", "approval"},
		3: {Dead, "dead", ""},
		4: {Dead, "dead", ""},
		5: {InReview, "alive", ""},
		6: {Waiting, "alive", ""},
	}
	for _, row := range l.Rows {
		n := int(row.Task[len(row.Task)-1] - '0')
		if got := (want{row.State, alive(row), row.WaitingFor}); got != wants[n] {
			t.Errorf("task %d: got %+v, want %+v", n, got, wants[n])
		}
	}
	if len(l.Warnings) != 0 || !slices.Equal(f.calls, []string{"devbox"}) {
		t.Errorf("warnings %q, calls %q: this host is asked only for its own claims", l.Warnings, f.calls)
	}
}

// A failed call is unknown, never dead: the rows keep their label state with
// null liveness, and the host warns once however many claims it holds.
func TestRemoteFailedCallIsUnknown(t *testing.T) {
	f := &fake{
		tasks: []tracker.ClaimedTask{
			onHost(task(1, "w-1", "engine:running"), "devbox"),
			onHost(task(2, "w-2", "engine:in-review"), "devbox"),
		},
		errs: map[string]error{"devbox": errors.New("host devbox is unreachable over ssh (me@devbox): no route")},
	}
	l := list(t, f)
	if l.Rows[0].State != Running || l.Rows[1].State != InReview || alive(l.Rows[0]) != "unknown" || alive(l.Rows[1]) != "unknown" {
		t.Errorf("rows %+v", l.Rows)
	}
	if len(l.Warnings) != 1 || !strings.Contains(l.Warnings[0], "status of devbox: host devbox is unreachable") {
		t.Errorf("warnings %q", l.Warnings)
	}
	if len(f.calls) != 1 {
		t.Errorf("calls %q: one call per host", f.calls)
	}
}

// When Claude Code could not list its sessions on the host, a session started
// by hand there is unknown, and the host warns; windows still say what they
// say.
func TestRemoteAgentsErrorIsUnknown(t *testing.T) {
	f := &fake{
		tasks: []tracker.ClaimedTask{
			onHost(task(1, "w-1", "engine:running"), "devbox"),
			onHost(byHand(task(2, "w-2", "engine:running")), "devbox"),
		},
		statuses: map[string]hostops.Status{"devbox": {
			Windows: []hostops.Window{win("w-1", "")}, AgentsError: "claude agents --json: exit status 1",
		}},
	}
	l := list(t, f)
	if alive(l.Rows[0]) != "alive" || l.Rows[0].State != Running {
		t.Errorf("row 0 %+v", l.Rows[0])
	}
	if alive(l.Rows[1]) != "unknown" || l.Rows[1].State != Running {
		t.Errorf("row 1 %+v", l.Rows[1])
	}
	if len(l.Warnings) != 1 || !strings.Contains(l.Warnings[0], "claude agents on devbox: claude agents --json: exit status 1") {
		t.Errorf("warnings %q", l.Warnings)
	}
}

// A session started by hand cannot be found on another host by its worktree,
// so a status that does not list it by name leaves it unknown.
func TestRemoteHandStartedSessionIsUnknownUnlessListed(t *testing.T) {
	f := &fake{
		tasks: []tracker.ClaimedTask{
			onHost(byHand(task(1, "w-1", "engine:running")), "devbox"),
			onHost(byHand(task(2, "w-2", "engine:running")), "devbox"),
		},
		statuses: map[string]hostops.Status{"devbox": {Agents: []session.Agent{agent("w-1", session.AgentBusy), {Name: "renamed", CWD: "/work/w-2"}}}},
		checkout: map[string]session.Checkout{"/work/w-2": {Branch: "task/w-2", Remote: "https://github.com/o/r"}},
	}
	l := list(t, f)
	if l.Rows[0].State != Working || alive(l.Rows[0]) != "alive" || l.Rows[1].State != Running || alive(l.Rows[1]) != "unknown" {
		t.Errorf("rows %+v", l.Rows)
	}
	if len(f.checkoutCalls) != 0 {
		t.Errorf("this host's git was asked about another's directories: %q", f.checkoutCalls)
	}
}

// Each host with a claim is asked once, this one too, hosts with no claim are
// not, and a failure on one host leaves the others settled.
func TestEachClaimHostIsAskedOnce(t *testing.T) {
	f := (&fake{
		tasks: []tracker.ClaimedTask{
			task(1, "w-1", "engine:running"),
			onHost(task(2, "w-2", "engine:running"), "devbox"),
			onHost(task(3, "w-3", "engine:running"), "devbox"),
			onHost(task(4, "w-4", "engine:running"), "ci"),
			onHost(task(5, "w-5", "engine:running"), ""),
		},
		errs: map[string]error{"ci": errors.New("unreachable")},
	}).on([]hostops.Window{win("w-1", "")})
	f.statuses["devbox"] = hostops.Status{Windows: []hostops.Window{win("w-2", ""), win("w-3", "")}}
	f.statuses["idle"] = hostops.Status{}
	l := list(t, f)
	slices.Sort(f.calls)
	if !slices.Equal(f.calls, []string{"ci", "devbox", "mac"}) {
		t.Errorf("calls %q", f.calls)
	}
	got := []string{alive(l.Rows[0]), alive(l.Rows[1]), alive(l.Rows[2]), alive(l.Rows[3]), alive(l.Rows[4])}
	if !slices.Equal(got, []string{"alive", "alive", "alive", "unknown", "unknown"}) {
		t.Errorf("liveness %q", got)
	}
}

// More hosts than the bound are all asked, none twice, and never more than
// the bound at once.
func TestStatusCallsAreBounded(t *testing.T) {
	f := &fake{statuses: map[string]hostops.Status{}}
	for i := range 3 * maxParallel {
		host := "h" + string(rune('a'+i))
		f.tasks = append(f.tasks, onHost(task(i+1, "w-"+host, "engine:running"), host))
		f.statuses[host] = hostops.Status{Windows: []hostops.Window{win("w-"+host, "")}}
	}
	l := list(t, f)
	if len(f.calls) != len(f.tasks) || f.peak > maxParallel {
		t.Errorf("%d calls for %d hosts, %d at once", len(f.calls), len(f.tasks), f.peak)
	}
	for _, row := range l.Rows {
		if alive(row) != "alive" {
			t.Errorf("row %+v", row)
		}
	}
}
