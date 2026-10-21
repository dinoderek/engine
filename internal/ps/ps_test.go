package ps

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

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

// fake is a Source over canned answers that counts every probe.
type fake struct {
	tasks    []tracker.ClaimedTask
	windows  []session.WindowInfo
	agents   []session.Agent
	checkout map[string]session.Checkout
	// Errors the probes fail with.
	claimedErr, windowsErr, agentsErr, checkoutErr error
	windowCalls, agentCalls                        int
	checkoutCalls                                  []string
}

func (f *fake) source() Source {
	return Source{
		Local: "mac",
		Claimed: func() ([]tracker.ClaimedTask, error) {
			return append([]tracker.ClaimedTask(nil), f.tasks...), f.claimedErr
		},
		Windows: func() ([]session.WindowInfo, error) { f.windowCalls++; return f.windows, f.windowsErr },
		Agents:  func() ([]session.Agent, error) { f.agentCalls++; return f.agents, f.agentsErr },
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

func window(name string) session.WindowInfo {
	return session.WindowInfo{Name: name, ID: "@1", PaneID: "%1"}
}

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
	waiting := agent("w-3", session.AgentWaiting)
	waiting.WaitingFor = "approval"
	f := &fake{
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
		windows: []session.WindowInfo{window("w-1"), window("w-2"), window("w-3"), window("w-4"), window("w-5"), window("w-8"), window("w-9"),
			{Name: "w-7", ID: "@7", PaneID: "%7", Dead: true}},
		agents: []session.Agent{
			agent("w-1", session.AgentBusy), agent("w-2", session.AgentIdle), waiting,
			agent("w-4", session.AgentIdle), agent("w-5", session.AgentBusy), agent("w-8", "unheard-of"),
		},
	}
	l := list(t, f)
	type want struct{ state, alive, waitingFor string }
	wants := map[int]want{
		1: {Working, "alive", ""},
		2: {Waiting, "alive", ""},
		3: {Waiting, "alive", "approval"},
		4: {NeedsHuman, "alive", ""},
		5: {InReview, "alive", ""},
		6: {Dead, "dead", ""}, // no window, no agent
		7: {Dead, "dead", ""}, // the window's command exited
		8: {InReview, "alive", ""},
		9: {NeedsHuman, "alive", ""}, // a window, but no agent: the label stands
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
	if f.windowCalls != 1 || f.agentCalls != 1 {
		t.Errorf("probes ran %d and %d times, want once each", f.windowCalls, f.agentCalls)
	}
}

func TestRunningWithoutAHarnessSignalKeepsItsLabel(t *testing.T) {
	f := &fake{tasks: []tracker.ClaimedTask{task(1, "w-1", "engine:running")}, windows: []session.WindowInfo{window("w-1")},
		agents: []session.Agent{agent("w-1", "unheard-of")}}
	r := list(t, f).Rows[0]
	if r.State != Running || alive(r) != "alive" {
		t.Errorf("got %s, %s", r.State, alive(r))
	}
}

func TestRemoteClaimsShowTheirLabelAndProbeNothing(t *testing.T) {
	f := &fake{tasks: []tracker.ClaimedTask{
		onHost(task(1, "w-1", "engine:running"), "devbox"),
		onHost(task(2, "w-2", "engine:needs-human"), "devbox"),
		onHost(task(3, "w-3", "engine:in-review"), "devbox"),
		onHost(task(4, "w-4", "engine:running"), ""), // the claim names no host
	}}
	l := list(t, f)
	want := []string{Running, NeedsHuman, InReview, Running}
	for i, r := range l.Rows {
		if r.State != want[i] || alive(r) != "unknown" {
			t.Errorf("row %d: %s, %s; want %s, unknown", i, r.State, alive(r), want[i])
		}
	}
	if f.windowCalls+f.agentCalls+len(f.checkoutCalls) != 0 || len(l.Warnings) != 0 {
		t.Errorf("probed: %d windows, %d agents, %v checkouts; warnings %v", f.windowCalls, f.agentCalls, f.checkoutCalls, l.Warnings)
	}
	if l.Rows[3].Host != "" {
		t.Errorf("host %q", l.Rows[3].Host)
	}
}

func TestAHandStartedSessionIsFoundByName(t *testing.T) {
	f := &fake{tasks: []tracker.ClaimedTask{byHand(task(1, "h-1", "engine:running"))}, agents: []session.Agent{agent("h-1", session.AgentBusy)}}
	r := list(t, f).Rows[0]
	if r.State != Working || alive(r) != "alive" {
		t.Errorf("got %s, %s", r.State, alive(r))
	}
	if f.windowCalls != 0 {
		t.Error("tmux was probed for a claim that is not in tmux")
	}
}

func TestAHandStartedSessionIsFoundByWorktree(t *testing.T) {
	f := &fake{
		tasks: []tracker.ClaimedTask{byHand(task(1, "h-1", "engine:running"))},
		agents: []session.Agent{
			{Name: "unrelated", CWD: "/not/git", Status: session.AgentIdle},
			{Name: "other branch", CWD: "/work/other", Status: session.AgentIdle},
			{Name: "other repo", CWD: "/work/elsewhere", Status: session.AgentIdle},
			{Name: "renamed by hand", CWD: "/work/h-1", Status: session.AgentBusy},
			{CWD: "", Status: session.AgentIdle},
		},
		checkout: map[string]session.Checkout{
			"/work/other":     {Branch: "main", Remote: "https://github.com/o/r"},
			"/work/elsewhere": {Branch: "task/h-1", Remote: "git@github.com:p/q.git"},
			"/work/h-1":       {Branch: "task/h-1", Remote: "git@github.com:o/R.git"},
		},
	}
	r := list(t, f).Rows[0]
	if r.State != Working || alive(r) != "alive" {
		t.Errorf("got %s, %s", r.State, alive(r))
	}
	if want := []string{"/not/git", "/work/other", "/work/elsewhere", "/work/h-1"}; !reflect.DeepEqual(f.checkoutCalls, want) {
		t.Errorf("checkouts probed: %v, want %v", f.checkoutCalls, want)
	}
}

func TestNameBeatsWorktree(t *testing.T) {
	f := &fake{
		tasks:    []tracker.ClaimedTask{byHand(task(1, "h-1", "engine:running"))},
		agents:   []session.Agent{{Name: "stale", CWD: "/work/h-1", Status: session.AgentIdle}, {Name: "h-1", CWD: "/elsewhere", Status: session.AgentBusy}},
		checkout: map[string]session.Checkout{"/work/h-1": {Branch: "task/h-1", Remote: "https://github.com/o/r.git"}},
	}
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
	f := &fake{tasks: []tracker.ClaimedTask{byHand(task(1, "h-1", "engine:running"))}, agents: []session.Agent{agent("elsewhere", session.AgentBusy)}}
	if r := list(t, f).Rows[0]; r.State != Running || alive(r) != "unknown" {
		t.Errorf("got %s, %s", r.State, alive(r))
	}
}

func TestATmuxClaimWithNoWindowIsDeadWhateverClaudeSays(t *testing.T) {
	f := &fake{tasks: []tracker.ClaimedTask{task(1, "w-1", "engine:running")}, agents: []session.Agent{agent("elsewhere", session.AgentBusy)}}
	if r := list(t, f).Rows[0]; r.State != Dead || alive(r) != "dead" {
		t.Errorf("got %s, %s", r.State, alive(r))
	}
}

func TestTheWindowIsTheOneTheClaimNames(t *testing.T) {
	c := task(1, "w-1", "engine:running")
	c.Claim.Tmux = "engine:renamed"
	f := &fake{tasks: []tracker.ClaimedTask{c}, windows: []session.WindowInfo{window("renamed")}}
	if r := list(t, f).Rows[0]; alive(r) != "alive" {
		t.Errorf("got %s, %s", r.State, alive(r))
	}
	c.Claim.Tmux = "other:w-1" // not engine's tmux session
	f = &fake{tasks: []tracker.ClaimedTask{c}, windows: []session.WindowInfo{window("w-1")}}
	if r := list(t, f).Rows[0]; alive(r) != "unknown" || f.windowCalls != 0 {
		t.Errorf("got %s, %s after %d window probes", r.State, alive(r), f.windowCalls)
	}
}

func TestClaudeIsRecognisedWhateverTheClaimCallsIt(t *testing.T) {
	for _, harness := range []string{"claude 2.1.302 (m)", "claude-code 2.1.302 (m)", "Claude Code 2.1.302 (m)"} {
		c := byHand(task(1, "h-1", "engine:running"))
		c.Claim.Harness = harness
		f := &fake{tasks: []tracker.ClaimedTask{c}, agents: []session.Agent{agent("h-1", session.AgentBusy)}}
		if r := list(t, f).Rows[0]; r.State != Working {
			t.Errorf("%q: got %s", harness, r.State)
		}
	}
}

func TestCodexSessionsAreSettledByTmuxAlone(t *testing.T) {
	codex := func(t tracker.ClaimedTask) tracker.ClaimedTask {
		t.Claim.Harness = "codex-cli 1.0 (gpt)"
		return t
	}
	f := &fake{
		tasks:   []tracker.ClaimedTask{codex(task(1, "x-1", "engine:running")), codex(task(2, "x-2", "engine:running")), byHand(codex(task(3, "x-3", "engine:running")))},
		windows: []session.WindowInfo{window("x-1")},
		agents:  []session.Agent{agent("x-2", session.AgentBusy)},
	}
	l := list(t, f)
	got := []string{l.Rows[0].State + "/" + alive(l.Rows[0]), l.Rows[1].State + "/" + alive(l.Rows[1]), l.Rows[2].State + "/" + alive(l.Rows[2])}
	if want := []string{"running/alive", "dead/dead", "running/unknown"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if f.agentCalls != 0 {
		t.Error("claude agents was asked about codex sessions")
	}
}

func TestAFailedProbeLeavesLivenessUnknown(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		name  string
		f     *fake
		state string
		alive string
		warn  string
	}{
		{"tmux fails, claude finds it", &fake{windowsErr: boom, agents: []session.Agent{agent("w-1", session.AgentBusy)}}, Working, "alive", "tmux: boom"},
		{"tmux fails, claude finds nothing", &fake{windowsErr: boom}, Running, "unknown", "tmux: boom"},
		{"claude fails, the window is open", &fake{agentsErr: boom, windows: []session.WindowInfo{window("w-1")}}, Running, "alive", "claude agents: boom"},
		{"claude fails, no window", &fake{agentsErr: boom}, Running, "unknown", "claude agents: boom"},
		{"git cannot run", &fake{checkoutErr: boom, agents: []session.Agent{agent("other", session.AgentIdle)}, checkout: map[string]session.Checkout{"/work/other": {}}}, Running, "unknown", "git: boom"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.f.tasks = []tracker.ClaimedTask{task(1, "w-1", "engine:running")}
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
}

func TestWarningsAreNotRepeated(t *testing.T) {
	f := &fake{windowsErr: errors.New("boom"), tasks: []tracker.ClaimedTask{task(1, "w-1", "engine:running"), task(2, "w-2", "engine:running")}}
	if l := list(t, f); len(l.Warnings) != 1 || f.windowCalls != 1 {
		t.Errorf("warnings %q after %d probes", l.Warnings, f.windowCalls)
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
	f := &fake{tasks: []tracker.ClaimedTask{task(42, "w-42", "engine:running")}, windows: []session.WindowInfo{window("w-42")}}
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

func TestAge(t *testing.T) {
	cases := map[time.Duration]string{
		0: "<1m", 59 * time.Second: "<1m", time.Minute: "1m", 59*time.Minute + 59*time.Second: "59m",
		time.Hour: "1h", 47*time.Hour + 59*time.Minute: "47h", 48 * time.Hour: "2d", 100 * time.Hour: "4d",
	}
	for d, want := range cases {
		if got := Age(d); got != want {
			t.Errorf("Age(%v) = %q, want %q", d, got, want)
		}
	}
}

func rows(t *testing.T) []Row {
	t.Helper()
	other := task(2, "w-2", "engine:in-review")
	other.Claim.Host, other.Claim.Harness = "", ""
	f := &fake{
		tasks:   []tracker.ClaimedTask{task(1, "w-1", "engine:running"), onHost(other, "devbox"), task(30, "w-30", "engine:running")},
		windows: []session.WindowInfo{window("w-1")},
		agents:  []session.Agent{{Name: "w-1", Status: session.AgentBusy}},
	}
	f.tasks[2].Claim.PostedAt = now.Add(-72 * time.Hour)
	return list(t, f).Rows
}

func TestWriteTable(t *testing.T) {
	var out bytes.Buffer
	if err := WriteTable(&out, rows(t)); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("got:\n%s", out.String())
	}
	header := strings.Fields(lines[0])
	if !reflect.DeepEqual(header, []string{"TASK", "HOST", "HARNESS", "STATE", "AGE"}) {
		t.Errorf("header %q", header)
	}
	for i, wantCells := range [][]string{
		{tracker.Ref{Repo: "o/r", Number: 1}.String(), "mac", "claude-code", "working", "1h"},
		{tracker.Ref{Repo: "o/r", Number: 2}.String(), "devbox", "-", "in-review", "1h"},
		{tracker.Ref{Repo: "o/r", Number: 30}.String(), "mac", "claude-code", "dead", "3d"},
	} {
		if got := strings.Fields(lines[i+1]); !reflect.DeepEqual(got, wantCells) {
			t.Errorf("row %d: got %q, want %q", i, got, wantCells)
		}
	}
	// Aligned: every column starts where the header's does.
	for _, col := range []string{"HOST", "HARNESS", "STATE", "AGE"} {
		at := strings.Index(lines[0], col)
		for _, l := range lines[1:] {
			if at > len(l) || l[at-1] != ' ' || l[at] == ' ' {
				t.Errorf("column %s is not aligned in %q", col, l)
			}
		}
	}
}

func TestWriteTableEmpty(t *testing.T) {
	var out bytes.Buffer
	if err := WriteTable(&out, nil); err != nil || out.String() != "no claimed tasks\n" {
		t.Errorf("got %q, %v", out.String(), err)
	}
}

func TestWriteJSONParsesBack(t *testing.T) {
	want := rows(t)
	var out bytes.Buffer
	if err := WriteJSON(&out, want); err != nil {
		t.Fatal(err)
	}
	var got []Row
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
	// Unknown liveness is null, not false, and a missing field is left out.
	var generic []map[string]any
	if err := json.Unmarshal(out.Bytes(), &generic); err != nil {
		t.Fatal(err)
	}
	if v, ok := generic[1]["alive"]; !ok || v != nil {
		t.Errorf("alive of a remote row: %v, %v", v, ok)
	}
	if _, ok := generic[1]["harness"]; ok {
		t.Error("an empty harness was written")
	}
	if generic[0]["alive"] != true || generic[2]["alive"] != false || generic[0]["state"] != Working {
		t.Errorf("rows: %v", generic)
	}
}

func TestWriteJSONEmptyIsAnArray(t *testing.T) {
	var out bytes.Buffer
	if err := WriteJSON(&out, nil); err != nil || strings.TrimSpace(out.String()) != "[]" {
		t.Errorf("got %q, %v", out.String(), err)
	}
}
