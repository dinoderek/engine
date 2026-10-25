package ps

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/tracker"
)

var now = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

// task is a claimed task by number, in repo o/r. Its claim is the way an
// `engine start` session records it: on host mac, in engine's tmux session.
func task(n int, name string, labels ...string) tracker.ClaimedTask {
	return tracker.ClaimedTask{
		Ref: tracker.Ref{Repo: "o/r", Number: n}, Title: "task " + name, Labels: labels,
		Claim: tracker.Claim{
			Name: name, Run: name + "-run", Host: "mac", Harness: "claude-code 2.1.302 (claude-sonnet-5-5)",
			Tmux: "engine:" + name, Branch: "task/" + name, PostedAt: now.Add(-90 * time.Minute),
		},
	}
}

func byHand(t tracker.ClaimedTask) tracker.ClaimedTask {
	t.Claim.Tmux, t.Claim.StartedBy = "", "hand"
	return t
}

func onHost(t tracker.ClaimedTask, host string) tracker.ClaimedTask {
	t.Claim.Host = host
	return t
}

// fake is a Source over canned statuses by host name, mac being this host,
// and canned checkouts on mac. A host with an error fails its call; a host
// with neither is not in hosts.toml.
type fake struct {
	tasks    []tracker.ClaimedTask
	statuses map[string]hostops.Status
	errs     map[string]error
	checkout map[string]session.Checkout
	// Errors the tracker and git fail with.
	claimedErr, checkoutErr error

	mu            sync.Mutex
	calls         []string
	inflight      int
	peak          int
	checkoutCalls []string
}

// on sets what mac's status says: its windows and live Claude Code sessions.
func (f *fake) on(windows []hostops.Window, agents ...session.Agent) *fake {
	if f.statuses == nil {
		f.statuses = map[string]hostops.Status{}
	}
	f.statuses["mac"] = hostops.Status{Windows: windows, Agents: agents}
	return f
}

func (f *fake) status(host string) (hostops.Status, error) {
	f.mu.Lock()
	f.calls = append(f.calls, host)
	f.inflight++
	f.peak = max(f.peak, f.inflight)
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.inflight--
		f.mu.Unlock()
	}()
	if err, ok := f.errs[host]; ok {
		return hostops.Status{}, err
	}
	if st, ok := f.statuses[host]; ok {
		return st, nil
	}
	return hostops.Status{}, errors.New("not in hosts.toml")
}

func (f *fake) source() Source {
	return Source{
		Local: "mac",
		Claimed: func() ([]tracker.ClaimedTask, error) {
			return append([]tracker.ClaimedTask(nil), f.tasks...), f.claimedErr
		},
		Status: f.status,
		Checkout: func(dir string) (session.Checkout, error) {
			f.checkoutCalls = append(f.checkoutCalls, dir)
			if co, ok := f.checkout[dir]; ok {
				return co, f.checkoutErr
			}
			if f.checkoutErr != nil {
				return session.Checkout{}, f.checkoutErr
			}
			return session.Checkout{}, &session.ExitError{Command: "git", Code: 128, Stderr: "not a git repository"}
		},
		Now: func() time.Time { return now },
	}
}

// win is a live window of a Claude session in the state status gives it.
func win(name, state string) hostops.Window {
	return hostops.Window{Name: name, ID: "@1", Alive: true, Harness: hostops.Harness, State: state}
}

func deadWin(name string) hostops.Window { return hostops.Window{Name: name, ID: "@7"} }

func agent(name, status string) session.Agent {
	return session.Agent{PID: 1, CWD: "/work/" + name, Name: name, Status: status}
}

func list(t *testing.T, f *fake) Listing {
	t.Helper()
	l, err := List(f.source())
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func alive(r Row) string {
	switch {
	case r.Alive == nil:
		return "unknown"
	case *r.Alive:
		return "alive"
	}
	return "dead"
}

func TestStates(t *testing.T) {
	waiting := win("w-3", session.StateWaiting)
	waiting.WaitingFor = "approval"
	f := (&fake{
		tasks: []tracker.ClaimedTask{
			task(1, "w-1", "engine:running"),
			task(2, "w-2", "engine:running"),
			task(3, "w-3", "engine:running"),
			task(4, "w-4", "engine:needs-human"),
			task(5, "w-5", "engine:in-review"),
			task(6, "w-6", "engine:running"),
			task(7, "w-7", "engine:running"),
			task(8, "w-8", "engine:running", "engine:in-review"),
			task(9, "w-9", "engine:running", "engine:needs-human"),
		},
	}).on([]hostops.Window{win("w-1", session.StateWorking), win("w-2", session.StateWaiting), waiting,
		win("w-4", session.StateWaiting), win("w-5", session.StateWorking), win("w-8", ""), win("w-9", ""), deadWin("w-7")})
	l := list(t, f)
	type want struct{ state, alive, waitingFor string }
	wants := map[int]want{
		1: {Working, "alive", ""},
		2: {Waiting, "alive", ""},
		3: {Waiting, "alive", "approval"},
		4: {NeedsHuman, "alive", ""},
		5: {InReview, "alive", ""},
		6: {Dead, "dead", ""}, // no window
		7: {Dead, "dead", ""}, // the window's command exited
		8: {InReview, "alive", ""},
		9: {NeedsHuman, "alive", ""}, // a window whose state is unknown: the label stands
	}
	if len(l.Rows) != len(wants) || len(l.Warnings) != 0 {
		t.Fatalf("got %d rows, warnings %v", len(l.Rows), l.Warnings)
	}
	for i, r := range l.Rows {
		w := wants[i+1]
		if r.State != w.state || alive(r) != w.alive || r.WaitingFor != w.waitingFor {
			t.Errorf("task %d: got %s, %s, %q; want %+v", i+1, r.State, alive(r), r.WaitingFor, w)
		}
	}
	if !slices.Equal(f.calls, []string{"mac"}) {
		t.Errorf("status calls %q, want mac once", f.calls)
	}
}

// A claim in engine's tmux session reads its window's state alone: Claude
// Code's raw list, whatever it says, settles nothing for it.
func TestAWindowedClaimReadsOnlyItsWindowsState(t *testing.T) {
	f := (&fake{tasks: []tracker.ClaimedTask{task(1, "w-1", "engine:running"), task(2, "w-2", "engine:running")}}).
		on([]hostops.Window{win("w-1", "")}, agent("w-1", session.AgentBusy), agent("w-2", session.AgentBusy))
	l := list(t, f)
	if r := l.Rows[0]; r.State != Running || alive(r) != "alive" {
		t.Errorf("unknown state: got %s, %s", r.State, alive(r))
	}
	if r := l.Rows[1]; r.State != Dead || alive(r) != "dead" {
		t.Errorf("no window: got %s, %s", r.State, alive(r))
	}
}

func TestClaimsNoHostCanAnswerShowTheirLabel(t *testing.T) {
	tasks := []tracker.ClaimedTask{
		onHost(task(1, "w-1", "engine:running"), "devbox"),
		onHost(task(2, "w-2", "engine:needs-human"), "devbox"),
		task(3, "w-3", "engine:in-review"),
		onHost(task(4, "w-4", "engine:running"), ""), // the claim names no host
	}
	f := &fake{tasks: tasks}
	src := f.source()
	src.Status = nil // nothing can be asked
	l, err := List(src)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{Running, NeedsHuman, InReview, Running}
	for i, r := range l.Rows {
		if r.State != want[i] || alive(r) != "unknown" {
			t.Errorf("row %d: %s, %s; want %s, unknown", i, r.State, alive(r), want[i])
		}
	}
	if len(f.calls)+len(f.checkoutCalls) != 0 || len(l.Warnings) != 0 {
		t.Errorf("probed: %v status, %v checkouts; warnings %v", f.calls, f.checkoutCalls, l.Warnings)
	}
	if l.Rows[3].Host != "" {
		t.Errorf("host %q", l.Rows[3].Host)
	}
	// A claim that names no host is never asked about.
	f = (&fake{tasks: tasks[3:]}).on(nil)
	if l := list(t, f); alive(l.Rows[0]) != "unknown" || len(f.calls) != 0 {
		t.Errorf("got %s after calls %q", alive(l.Rows[0]), f.calls)
	}
}

func TestAHandStartedSessionIsFoundByName(t *testing.T) {
	f := (&fake{tasks: []tracker.ClaimedTask{byHand(task(1, "h-1", "engine:running"))}}).on(nil, agent("h-1", session.AgentBusy))
	r := list(t, f).Rows[0]
	if r.State != Working || alive(r) != "alive" {
		t.Errorf("got %s, %s", r.State, alive(r))
	}
}

func TestAHandStartedSessionIsFoundByWorktree(t *testing.T) {
	f := (&fake{
		tasks: []tracker.ClaimedTask{byHand(task(1, "h-1", "engine:running"))},
		checkout: map[string]session.Checkout{
			"/work/other":     {Branch: "main", Remote: "https://github.com/o/r"},
			"/work/elsewhere": {Branch: "task/h-1", Remote: "git@github.com:p/q.git"},
			"/work/h-1":       {Branch: "task/h-1", Remote: "git@github.com:o/R.git"},
		},
	}).on(nil,
		session.Agent{Name: "unrelated", CWD: "/not/git", Status: session.AgentIdle},
		session.Agent{Name: "other branch", CWD: "/work/other", Status: session.AgentIdle},
		session.Agent{Name: "other repo", CWD: "/work/elsewhere", Status: session.AgentIdle},
		session.Agent{Name: "renamed by hand", CWD: "/work/h-1", Status: session.AgentBusy},
		session.Agent{CWD: "", Status: session.AgentIdle},
	)
	r := list(t, f).Rows[0]
	if r.State != Working || alive(r) != "alive" {
		t.Errorf("got %s, %s", r.State, alive(r))
	}
	if want := []string{"/not/git", "/work/other", "/work/elsewhere", "/work/h-1"}; !reflect.DeepEqual(f.checkoutCalls, want) {
		t.Errorf("checkouts probed: %v, want %v", f.checkoutCalls, want)
	}
}

func TestNameBeatsWorktree(t *testing.T) {
	f := (&fake{
		tasks:    []tracker.ClaimedTask{byHand(task(1, "h-1", "engine:running"))},
		checkout: map[string]session.Checkout{"/work/h-1": {Branch: "task/h-1", Remote: "https://github.com/o/r.git"}},
	}).on(nil, session.Agent{Name: "stale", CWD: "/work/h-1", Status: session.AgentIdle}, session.Agent{Name: "h-1", CWD: "/elsewhere", Status: session.AgentBusy})
	if r := list(t, f).Rows[0]; r.State != Working {
		t.Errorf("got %s", r.State)
	}
	if len(f.checkoutCalls) != 0 {
		t.Errorf("probed checkouts %v despite a name match", f.checkoutCalls)
	}
}

func TestAHandStartedSessionNoAgentMatchesIsNotDead(t *testing.T) {
	// It may run under another name, in another directory, or for several
	// tasks at once: only a vanished tmux window proves a session gone.
	f := (&fake{tasks: []tracker.ClaimedTask{byHand(task(1, "h-1", "engine:running"))}}).on(nil, agent("elsewhere", session.AgentBusy))
	if r := list(t, f).Rows[0]; r.State != Running || alive(r) != "unknown" {
		t.Errorf("got %s, %s", r.State, alive(r))
	}
}

func TestTheWindowIsTheOneTheClaimNames(t *testing.T) {
	c := task(1, "w-1", "engine:running")
	c.Claim.Tmux = "engine:renamed"
	f := (&fake{tasks: []tracker.ClaimedTask{c}}).on([]hostops.Window{win("renamed", session.StateWorking)})
	if r := list(t, f).Rows[0]; r.State != Working {
		t.Errorf("by name: got %s, %s", r.State, alive(r))
	}
	c.Claim.Tmux = "engine:@4" // an id, which tmux reuses: not a name
	f = (&fake{tasks: []tracker.ClaimedTask{c}}).on([]hostops.Window{{Name: "w-1", ID: "@4", Alive: true, State: session.StateWaiting}})
	if r := list(t, f).Rows[0]; r.State != Dead {
		t.Errorf("by id: got %s, %s", r.State, alive(r))
	}
	c.Claim.Tmux = "other:w-1" // not engine's tmux session
	f = (&fake{tasks: []tracker.ClaimedTask{c}}).on([]hostops.Window{win("w-1", session.StateWorking)})
	if r := list(t, f).Rows[0]; alive(r) != "unknown" {
		t.Errorf("elsewhere: got %s, %s", r.State, alive(r))
	}
}

func TestClaudeIsRecognisedWhateverTheClaimCallsIt(t *testing.T) {
	for _, harness := range []string{"claude 2.1.302 (m)", "claude-code 2.1.302 (m)", "Claude Code 2.1.302 (m)"} {
		c := byHand(task(1, "h-1", "engine:running"))
		c.Claim.Harness = harness
		f := (&fake{tasks: []tracker.ClaimedTask{c}}).on(nil, agent("h-1", session.AgentBusy))
		if r := list(t, f).Rows[0]; r.State != Working {
			t.Errorf("%q: got %s", harness, r.State)
		}
	}
}

// A Codex session in engine's tmux session shows the state status read from
// its state file. One started by hand cannot be found: Claude Code's list
// is never asked about it.
func TestCodexRowsReadTheirWindowsState(t *testing.T) {
	codex := func(t tracker.ClaimedTask) tracker.ClaimedTask {
		t.Claim.Harness = "codex-cli 0.163.0 (gpt)"
		return t
	}
	cw := func(name, state string) hostops.Window {
		return hostops.Window{Name: name, ID: "@1", Alive: true, Harness: hostops.Codex, State: state}
	}
	f := (&fake{tasks: []tracker.ClaimedTask{
		codex(task(1, "x-1", "engine:running")), codex(task(2, "x-2", "engine:running")), codex(task(3, "x-3", "engine:running")),
		codex(task(4, "x-4", "engine:running")), byHand(codex(task(5, "x-5", "engine:running"))),
	}}).on([]hostops.Window{cw("x-1", session.StateWorking), cw("x-2", session.StateWaiting), cw("x-3", "")},
		agent("x-4", session.AgentBusy), agent("x-5", session.AgentBusy))
	l := list(t, f)
	var got []string
	for _, r := range l.Rows {
		got = append(got, r.State+"/"+alive(r))
	}
	if want := []string{"working/alive", "waiting/alive", "running/alive", "dead/dead", "running/unknown"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestAFailedProbeLeavesLivenessUnknown(t *testing.T) {
	boom := errors.New("boom")
	failing := &fake{errs: map[string]error{"mac": boom}}
	agentsDown := &fake{statuses: map[string]hostops.Status{"mac": {Windows: []hostops.Window{win("w-1", "")}, AgentsError: "boom"}}}
	gitDown := (&fake{checkoutErr: boom, checkout: map[string]session.Checkout{"/work/other": {}}}).on(nil, agent("other", session.AgentIdle))
	cases := []struct {
		name  string
		f     *fake
		claim tracker.ClaimedTask
		state string
		alive string
		warn  string
	}{
		{"status fails", failing, task(1, "w-1", "engine:running"), Running, "unknown", "status of mac: boom"},
		{"status fails, by hand", failing, byHand(task(1, "w-1", "engine:running")), Running, "unknown", "status of mac: boom"},
		{"claude fails, by hand", agentsDown, byHand(task(1, "w-1", "engine:running")), Running, "unknown", "claude agents on mac: boom"},
		{"git cannot run", gitDown, byHand(task(1, "w-1", "engine:running")), Running, "unknown", "git: boom"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.f.tasks = []tracker.ClaimedTask{tc.claim}
			l := list(t, tc.f)
			r := l.Rows[0]
			if r.State != tc.state || alive(r) != tc.alive {
				t.Errorf("got %s, %s; want %s, %s", r.State, alive(r), tc.state, tc.alive)
			}
			if len(l.Warnings) != 1 || !strings.HasPrefix(l.Warnings[0], tc.warn) {
				t.Errorf("warnings %q, want one starting %q", l.Warnings, tc.warn)
			}
		})
	}
	// A window's state does not need Claude Code's list.
	agentsDown.tasks = []tracker.ClaimedTask{task(1, "w-1", "engine:running")}
	if l := list(t, agentsDown); alive(l.Rows[0]) != "alive" || len(l.Warnings) != 0 {
		t.Errorf("windowed with claude down: %s, warnings %q", alive(l.Rows[0]), l.Warnings)
	}
}

func TestWarningsAreNotRepeated(t *testing.T) {
	f := &fake{errs: map[string]error{"mac": errors.New("boom")}, tasks: []tracker.ClaimedTask{task(1, "w-1", "engine:running"), task(2, "w-2", "engine:running")}}
	if l := list(t, f); len(l.Warnings) != 1 || len(f.calls) != 1 {
		t.Errorf("warnings %q after calls %q", l.Warnings, f.calls)
	}
}

func TestListFailsWhenTheTasksCannotBeRead(t *testing.T) {
	f := &fake{claimedErr: errors.New("gh is down")}
	if _, err := List(f.source()); err == nil || !strings.Contains(err.Error(), "gh is down") {
		t.Errorf("got %v", err)
	}
}

func TestRowsAreOrderedByRepoThenNumber(t *testing.T) {
	a10, a9, b1 := task(10, "a-10"), task(9, "a-9"), task(1, "b-1")
	b1.Ref.Repo = "p/q"
	f := &fake{tasks: []tracker.ClaimedTask{b1, a10, a9}}
	var got []string
	for _, r := range list(t, f).Rows {
		got = append(got, r.Session)
	}
	if want := []string{"a-9", "a-10", "b-1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestRowFields(t *testing.T) {
	f := (&fake{tasks: []tracker.ClaimedTask{task(42, "w-42", "engine:running")}}).on([]hostops.Window{win("w-42", "")})
	got := list(t, f).Rows[0]
	yes := true
	want := Row{
		Task: tracker.Ref{Repo: "o/r", Number: 42}.String(), Title: "task w-42", Session: "w-42", Run: "w-42-run", Host: "mac",
		Harness: "claude-code", Branch: "task/w-42", State: Running, Alive: &yes,
		ClaimedAt: "2026-10-21T10:30:00Z", AgeSeconds: 5400,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func TestAClaimFromTheFutureHasNoNegativeAge(t *testing.T) {
	c := task(1, "w-1", "engine:running")
	c.Claim.PostedAt = now.Add(time.Hour)
	if r := claimRow(c, now); r.AgeSeconds != 0 {
		t.Errorf("age %d", r.AgeSeconds)
	}
}
