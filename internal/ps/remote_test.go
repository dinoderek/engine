package ps

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/tracker"
)

// remotes is a Source.Remote over canned statuses by host name. A host with
// an error fails its call; a host with neither is not in hosts.toml.
type remotes struct {
	mu       sync.Mutex
	statuses map[string]hostops.Status
	errs     map[string]error
	calls    []string
	inflight int
	peak     int
}

func (r *remotes) status(host string) (hostops.Status, error) {
	r.mu.Lock()
	r.calls = append(r.calls, host)
	r.inflight++
	r.peak = max(r.peak, r.inflight)
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.inflight--
		r.mu.Unlock()
	}()
	if err, ok := r.errs[host]; ok {
		return hostops.Status{}, err
	}
	if st, ok := r.statuses[host]; ok {
		return st, nil
	}
	return hostops.Status{}, errors.New("not in hosts.toml")
}

func hostWindow(name string, alive bool) hostops.Window {
	return hostops.Window{Name: name, ID: "@1", Alive: alive}
}

func listRemote(t *testing.T, f *fake, r *remotes) Listing {
	t.Helper()
	src := f.source()
	src.Remote = r.status
	l, err := List(src)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// A successful status settles a remote claim as a local one: alive and busy
// is working, alive and idle is waiting, and a window that is gone, or whose
// command exited, is dead.
func TestRemoteRowsAreSettledFromStatus(t *testing.T) {
	waiting := agent("w-2", session.AgentWaiting)
	waiting.WaitingFor = "approval"
	f := &fake{tasks: []tracker.ClaimedTask{
		onHost(task(1, "w-1", "engine:running"), "devbox"),
		onHost(task(2, "w-2", "engine:running"), "devbox"),
		onHost(task(3, "w-3", "engine:running"), "devbox"),
		onHost(task(4, "w-4", "engine:running"), "devbox"),
		onHost(task(5, "w-5", "engine:in-review"), "devbox"),
	}}
	r := &remotes{statuses: map[string]hostops.Status{"devbox": {
		Windows: []hostops.Window{hostWindow("w-1", true), hostWindow("w-2", true), hostWindow("w-4", false), hostWindow("w-5", true)},
		Agents:  []session.Agent{agent("w-1", session.AgentBusy), waiting},
	}}}
	l := listRemote(t, f, r)
	type want struct{ state, alive, waitingFor string }
	wants := map[int]want{
		1: {Working, "alive", ""},
		2: {Waiting, "alive", "approval"},
		3: {Dead, "dead", ""},
		4: {Dead, "dead", ""},
		5: {InReview, "alive", ""},
	}
	for _, row := range l.Rows {
		n := int(row.Task[len(row.Task)-1] - '0')
		if got := (want{row.State, alive(row), row.WaitingFor}); got != wants[n] {
			t.Errorf("task %d: got %+v, want %+v", n, got, wants[n])
		}
	}
	if len(l.Warnings) != 0 || !slices.Equal(r.calls, []string{"devbox"}) {
		t.Errorf("warnings %q, calls %q", l.Warnings, r.calls)
	}
	if f.windowCalls != 0 || f.agentCalls != 0 {
		t.Errorf("this host was probed for a claim on another: %d windows, %d agents", f.windowCalls, f.agentCalls)
	}
}

// A failed call is unknown, never dead: the rows keep their label state with
// null liveness, and the host warns once however many claims it holds.
func TestRemoteFailedCallIsUnknown(t *testing.T) {
	f := &fake{tasks: []tracker.ClaimedTask{
		onHost(task(1, "w-1", "engine:running"), "devbox"),
		onHost(task(2, "w-2", "engine:in-review"), "devbox"),
	}}
	r := &remotes{errs: map[string]error{"devbox": errors.New("host devbox is unreachable over ssh (me@devbox): no route")}}
	l := listRemote(t, f, r)
	if l.Rows[0].State != Running || l.Rows[1].State != InReview || alive(l.Rows[0]) != "unknown" || alive(l.Rows[1]) != "unknown" {
		t.Errorf("rows %+v", l.Rows)
	}
	if len(l.Warnings) != 1 || !strings.Contains(l.Warnings[0], "status of devbox: host devbox is unreachable") {
		t.Errorf("warnings %q", l.Warnings)
	}
	if len(r.calls) != 1 {
		t.Errorf("calls %q: one call per host", r.calls)
	}
}

// When Claude Code could not list its sessions on the host, a session whose
// window is gone is not dead, and a window that is there still shows alive.
func TestRemoteAgentsErrorIsUnknown(t *testing.T) {
	f := &fake{tasks: []tracker.ClaimedTask{
		onHost(task(1, "w-1", "engine:running"), "devbox"),
		onHost(task(2, "w-2", "engine:running"), "devbox"),
	}}
	r := &remotes{statuses: map[string]hostops.Status{"devbox": {
		Windows: []hostops.Window{hostWindow("w-1", true)}, AgentsError: "claude agents --json: exit status 1",
	}}}
	l := listRemote(t, f, r)
	if alive(l.Rows[0]) != "alive" || l.Rows[0].State != Running {
		t.Errorf("row 0 %+v", l.Rows[0])
	}
	if alive(l.Rows[1]) != "unknown" || l.Rows[1].State != Running {
		t.Errorf("row 1 %+v: a window that is gone is not dead while the agent list is unknown", l.Rows[1])
	}
	if len(l.Warnings) != 1 || !strings.Contains(l.Warnings[0], "claude agents on devbox: claude agents --json: exit status 1") {
		t.Errorf("warnings %q", l.Warnings)
	}
}

// A session started by hand cannot be found on another host by its worktree,
// so a status that does not list it by name leaves it unknown.
func TestRemoteHandStartedSessionIsUnknownUnlessListed(t *testing.T) {
	f := &fake{tasks: []tracker.ClaimedTask{
		onHost(byHand(task(1, "w-1", "engine:running")), "devbox"),
		onHost(byHand(task(2, "w-2", "engine:running")), "devbox"),
	}}
	r := &remotes{statuses: map[string]hostops.Status{"devbox": {Agents: []session.Agent{agent("w-1", session.AgentBusy)}}}}
	l := listRemote(t, f, r)
	if l.Rows[0].State != Working || alive(l.Rows[0]) != "alive" || l.Rows[1].State != Running || alive(l.Rows[1]) != "unknown" {
		t.Errorf("rows %+v", l.Rows)
	}
}

// Each host with a claim is asked once, hosts with no claim and this host are
// not, and a failure on one host leaves the others settled.
func TestRemoteAsksEachClaimHostOnce(t *testing.T) {
	f := &fake{
		tasks: []tracker.ClaimedTask{
			task(1, "w-1", "engine:running"),
			onHost(task(2, "w-2", "engine:running"), "devbox"),
			onHost(task(3, "w-3", "engine:running"), "devbox"),
			onHost(task(4, "w-4", "engine:running"), "ci"),
			onHost(task(5, "w-5", "engine:running"), ""),
		},
		windows: []session.WindowInfo{window("w-1")},
	}
	r := &remotes{
		statuses: map[string]hostops.Status{"devbox": {Windows: []hostops.Window{hostWindow("w-2", true), hostWindow("w-3", true)}}},
		errs:     map[string]error{"ci": errors.New("unreachable")},
	}
	l := listRemote(t, f, r)
	slices.Sort(r.calls)
	if !slices.Equal(r.calls, []string{"ci", "devbox"}) {
		t.Errorf("calls %q", r.calls)
	}
	got := []string{alive(l.Rows[0]), alive(l.Rows[1]), alive(l.Rows[2]), alive(l.Rows[3]), alive(l.Rows[4])}
	if !slices.Equal(got, []string{"alive", "alive", "alive", "unknown", "unknown"}) {
		t.Errorf("liveness %q", got)
	}
}

// With no way to ask, a claim on another host keeps its label state.
func TestNoRemoteLeavesOtherHostsUnsettled(t *testing.T) {
	f := &fake{tasks: []tracker.ClaimedTask{onHost(task(1, "w-1", "engine:running"), "devbox")}}
	if l := list(t, f); l.Rows[0].State != Running || alive(l.Rows[0]) != "unknown" || len(l.Warnings) != 0 {
		t.Errorf("rows %+v, warnings %q", l.Rows, l.Warnings)
	}
}

// More hosts than the bound are all asked, none twice, and never more than
// the bound at once.
func TestRemoteBoundsParallelCalls(t *testing.T) {
	var tasks []tracker.ClaimedTask
	r := &remotes{statuses: map[string]hostops.Status{}}
	for i := range 3 * maxParallel {
		host := "h" + string(rune('a'+i))
		tasks = append(tasks, onHost(task(i+1, "w-"+host, "engine:running"), host))
		r.statuses[host] = hostops.Status{Windows: []hostops.Window{hostWindow("w-"+host, true)}}
	}
	l := listRemote(t, &fake{tasks: tasks}, r)
	if len(r.calls) != len(tasks) || r.peak > maxParallel {
		t.Errorf("%d calls for %d hosts, %d at once", len(r.calls), len(tasks), r.peak)
	}
	for _, row := range l.Rows {
		if alive(row) != "alive" {
			t.Errorf("row %+v", row)
		}
	}
}
