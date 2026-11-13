package claude

import (
	"errors"
	"reflect"
	"strings"
	"testing"

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
