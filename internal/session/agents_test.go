package session

import (
	"errors"
	"os/exec"
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
		{PID: 58375, CWD: "/work/a", Name: "a-7", Status: AgentIdle},
		{PID: 50701, CWD: "/work/b", Name: "b-9", Status: AgentBusy},
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

func TestProbeCheckout(t *testing.T) {
	var ran []string
	run := func(name string, args ...string) (string, error) {
		ran = append(ran, name+" "+strings.Join(args, " "))
		if args[2] == "rev-parse" {
			return "task/a-1\n", nil
		}
		return "git@github.com:o/r.git\n", nil
	}
	got, err := ProbeCheckout(run, "/work/a")
	if err != nil || got != (Checkout{Branch: "task/a-1", Remote: "git@github.com:o/r.git"}) {
		t.Errorf("got %+v, %v", got, err)
	}
	want := []string{"git -C /work/a rev-parse --abbrev-ref HEAD", "git -C /work/a remote get-url origin"}
	if !reflect.DeepEqual(ran, want) {
		t.Errorf("ran %q", ran)
	}
}

func TestProbeCheckoutDetachedHead(t *testing.T) {
	run := func(_ string, args ...string) (string, error) {
		if args[2] == "rev-parse" {
			return "HEAD\n", nil
		}
		return "https://github.com/o/r\n", nil
	}
	if got, err := ProbeCheckout(run, "/w"); err != nil || got.Branch != "" {
		t.Errorf("got %+v, %v", got, err)
	}
}

func TestProbeCheckoutFailures(t *testing.T) {
	notRepo := &ExitError{Command: "git", Code: 128, Stderr: "fatal: not a git repository"}
	for name, failAt := range map[string]string{"not a checkout": "rev-parse", "no origin": "remote"} {
		run := func(_ string, args ...string) (string, error) {
			if args[2] == failAt {
				return "", notRepo
			}
			return "x\n", nil
		}
		var exit *ExitError
		if _, err := ProbeCheckout(run, "/w"); !errors.As(err, &exit) {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

func TestProbeCheckoutInARealRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "task/real-1"},
		{"remote", "add", "origin", "https://github.com/o/r.git"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "x"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	got, err := ProbeCheckout(Exec, dir)
	if err != nil || got.Branch != "task/real-1" || !got.IsRepo("o/r") {
		t.Fatalf("got %+v, %v", got, err)
	}
	var exit *ExitError
	if _, err := ProbeCheckout(Exec, t.TempDir()); !errors.As(err, &exit) {
		t.Errorf("a directory outside a checkout: %v", err)
	}
}

func TestCheckoutIsRepo(t *testing.T) {
	cases := []struct {
		remote string
		want   bool
	}{
		{"https://github.com/o/r", true},
		{"https://github.com/o/r.git", true},
		{"https://github.com/o/r/", true},
		{"git@github.com:o/r.git", true},
		{"ssh://git@github.com/O/R.git", true},
		{"https://github.com/o/rr", false},
		{"https://github.com/oo/r", false},
		{"https://github.com/p/q", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := (Checkout{Remote: tc.remote}).IsRepo("o/r"); got != tc.want {
			t.Errorf("%q: got %v, want %v", tc.remote, got, tc.want)
		}
	}
}
