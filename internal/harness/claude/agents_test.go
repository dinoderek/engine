package claude

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/hosts"

	"github.com/dinoderek/engine/internal/session"
)

// claudeAgents is the shape `claude agents --json` prints: the fields engine
// does not read are here so a decode that choked on them would fail.
const claudeAgents = `[
  {"pid": 58375, "cwd": "/work/a", "kind": "interactive", "startedAt": 1700000000001,
   "sessionId": "11111111-1111-4111-8111-111111111111", "name": "a-7", "status": "idle"},
  {"pid": 50701, "cwd": "/work/b", "kind": "interactive", "startedAt": 1700000000002,
   "sessionId": "22222222-2222-4222-8222-222222222222", "name": "b-9", "status": "busy"},
  {"pid": 5, "cwd": "/work/c", "name": "c-1", "status": "waiting", "waitingFor": "approval"}
]`

func TestListAgents(t *testing.T) {
	var ran []string
	run := func(name string, args ...string) (string, error) {
		ran = append(ran, name+" "+strings.Join(args, " "))
		return claudeAgents, nil
	}
	got, err := ListAgents(run, "/bin/claude")
	if err != nil {
		t.Fatal(err)
	}
	want := []Agent{
		{PID: 58375, CWD: "/work/a", Name: "a-7", SessionID: "11111111-1111-4111-8111-111111111111", Status: AgentIdle},
		{PID: 50701, CWD: "/work/b", Name: "b-9", SessionID: "22222222-2222-4222-8222-222222222222", Status: AgentBusy},
		{PID: 5, CWD: "/work/c", Name: "c-1", Status: AgentWaiting, WaitingFor: "approval"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
	if !reflect.DeepEqual(ran, []string{"/bin/claude agents --json"}) {
		t.Errorf("ran %q", ran)
	}
}

func TestListAgentsNone(t *testing.T) {
	got, err := ListAgents(func(string, ...string) (string, error) { return "[]\n", nil }, "claude")
	if err != nil || len(got) != 0 {
		t.Errorf("got %v, %v", got, err)
	}
}

func TestListAgentsFailures(t *testing.T) {
	fail := func(string, ...string) (string, error) { return "", errors.New("exec: not found") }
	if _, err := ListAgents(fail, "claude"); err == nil || !strings.Contains(err.Error(), "claude agents: exec: not found") {
		t.Errorf("run failure: %v", err)
	}
	for _, out := range []string{"", "No sessions", `{"pid": 1}`, `[{"pid": "x"}]`} {
		bad := func(string, ...string) (string, error) { return out, nil }
		if _, err := ListAgents(bad, "claude"); err == nil || !strings.Contains(err.Error(), "unreadable output") {
			t.Errorf("output %q: %v", out, err)
		}
	}
}

func TestAgentState(t *testing.T) {
	for status, want := range map[string]string{
		AgentBusy: session.StateWorking, AgentWaiting: session.StateWaiting, AgentIdle: session.StateWaiting, "unheard-of": "", "": "",
	} {
		if got := AgentState(Agent{Status: status}); got != want {
			t.Errorf("%q: got %q, want %q", status, got, want)
		}
	}
}

// A run's session is the one its launch named, by the window's name or the
// run's title, else the one in its worktree: the apps can rename a session.
func TestFindAgent(t *testing.T) {
	run := session.Run{Record: session.Record{Name: "r-7", Title: "t7 · Fix it", Worktree: "/w/r-7"}}
	tests := []struct {
		name   string
		run    session.Run
		agents []Agent
		want   int // the PID found; 0 is none
	}{
		{"by the window's name", run, []Agent{{PID: 1, Name: "other"}, {PID: 2, Name: "r-7"}}, 2},
		{"by the run's title", run, []Agent{{PID: 1, Name: "t7 · Fix it", CWD: "/elsewhere"}}, 1},
		{"renamed, by its worktree", run, []Agent{{PID: 1, Name: "Renamed", CWD: "/w/other"}, {PID: 2, Name: "Renamed", CWD: "/w/r-7/"}}, 2},
		{"a name beats another session's worktree", run, []Agent{{PID: 1, CWD: "/w/r-7"}, {PID: 2, Name: "t7 · Fix it"}}, 2},
		{"no title matches no unnamed session", session.Run{Record: session.Record{Name: "r-7"}}, []Agent{{PID: 1}}, 0},
		{"none", run, []Agent{{PID: 1, Name: "r-70", CWD: "/w/r-70"}}, 0},
	}
	for _, tt := range tests {
		ag, ok := findAgent(tt.run.Record.Name, tt.run, tt.agents)
		if got := map[bool]int{true: ag.PID}[ok]; got != tt.want {
			t.Errorf("%s: found %d, want %d", tt.name, got, tt.want)
		}
	}
}

// A session Claude Code has not listed by StartupWait is held at a dialog
// before it started, such as a trust prompt; before then, or when no list
// was read, it is unknown. A limit marker still says limited.
func TestProbeAtAStartupDialog(t *testing.T) {
	run := session.Run{Dir: t.TempDir(), Record: session.Record{Name: "r-7", Worktree: "/w/r-7"}}
	if st, ok := probe("r-7", run, nil, true); !ok || st.State != session.StateWaiting || !st.Dialog || st.WaitingFor == "" {
		t.Errorf("unlisted past StartupWait: %+v, %v; want waiting at a dialog", st, ok)
	}
	if st, ok := probe("r-7", run, nil, false); ok {
		t.Errorf("unlisted, just started: %+v; want unknown", st)
	}
	listed := []Agent{{Name: "r-7", Status: AgentWaiting, WaitingFor: "approval"}}
	if st, ok := probe("r-7", run, listed, true); !ok || st.Dialog || st.WaitingFor != "approval" {
		t.Errorf("listed: %+v, %v; want its own state", st, ok)
	}
	if err := os.WriteFile(filepath.Join(run.Dir, LimitFile), []byte(`{"since": 1, "resets_at": 1700000000, "error": "rate_limit"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if st, _ := probe("r-7", run, nil, true); st.State != session.StateLimited || st.Dialog {
		t.Errorf("unlisted with a limit marker: %+v; want limited", st)
	}
}

// Observe reads a session as at a dialog only from a list it read, once the
// run is StartupWait old.
func TestObserveFindsASessionAtAStartupDialog(t *testing.T) {
	now := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	exec := func(_ string, args ...string) (string, error) {
		if args[0] == "agents" {
			return "[]", nil
		}
		return `{"loggedIn": true}`, nil
	}
	env := harness.Env{Exec: exec, Home: t.TempDir(), Now: func() time.Time { return now }}
	host := hosts.Host{Name: "h", Harnesses: []string{Name}, Bin: map[string]string{Name: "/bin/claude"}}
	runs := map[string]session.Run{
		"old": {Dir: t.TempDir(), Record: session.Record{Name: "old", Spawned: now.Add(-StartupWait)}},
		"new": {Dir: t.TempDir(), Record: session.Record{Name: "new", Spawned: now.Add(-StartupWait + time.Second)}},
	}
	snap := New(env).Observe(context.Background(), host, runs)
	if st := snap.Sessions["old"]; !st.Dialog || st.State != session.StateWaiting {
		t.Errorf("old: %+v; want at a dialog", st)
	}
	if st, ok := snap.Sessions["new"]; ok {
		t.Errorf("new: %+v; want unknown", st)
	}
	other := New(env).Observe(context.Background(), hosts.Host{Name: "h"}, runs)
	if st, ok := other.Sessions["old"]; ok {
		t.Errorf("a host without Claude: %+v; want unknown", st)
	}
}
