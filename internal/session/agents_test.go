package session

import (
	"errors"
	"reflect"
	"strings"
	"testing"
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
