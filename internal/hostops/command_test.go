package hostops

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/version"
)

// call runs engine host with args and stdin, and decodes its one JSON
// object.
func call(t *testing.T, d Deps, stdin string, args ...string) (int, map[string]any, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Main(args, d, strings.NewReader(stdin), &out, &errOut)
	if code == 2 {
		return code, nil, errOut.String()
	}
	if strings.Count(out.String(), "\n") != 1 {
		t.Fatalf("want one line of JSON, got %q", out.String())
	}
	var reply map[string]any
	if err := json.Unmarshal(out.Bytes(), &reply); err != nil {
		t.Fatalf("%q: %v", out.String(), err)
	}
	return code, reply, errOut.String()
}

func TestEveryReplyCarriesTheEnvelope(t *testing.T) {
	h := newHome(t, "mac")
	writeRun(t, h, session.Record{RunID: "r-7-a", Name: "r-7", Spawned: fixedNow()})
	d := Deps{Tmux: (&tmuxFake{}).tmux(), Home: h.dir, Exec: agentsFake("[]", nil), Run: (&gitFake{}).run}
	engine := strings.TrimPrefix(version.Current().String(), "engine ")
	for _, args := range [][]string{
		{"status", "--json"},
		{"collect", "--run-id", "r-7-a", "--json"},
		{"release", "--json", "--name", "r-7", "--failed"},
		{"sync-repo", "--repo", "o", "--json"}, // a failure is a reply too
	} {
		_, reply, _ := call(t, d, "", args...)
		if reply["protocol"] != 1.0 || reply["engine"] != engine || reply["host"] != "mac" {
			t.Errorf("%v: envelope %v", args, reply)
		}
	}
	_, reply, _ := call(t, d, "", "status", "--json")
	for _, key := range []string{"slots", "free_slots", "windows", "agents", "runs"} {
		if _, ok := reply[key]; !ok {
			t.Errorf("status lacks %s: %v", key, reply)
		}
	}
}

func TestFailuresAreReplies(t *testing.T) {
	h := newHome(t, "mac")
	d := Deps{Tmux: (&tmuxFake{}).tmux(), Home: h.dir, Run: noGit(t)}
	cases := []struct {
		args    []string
		refused bool
		want    string
	}{
		{[]string{"sync-repo", "--json", "--repo", "o"}, true, `want owner/repo, got "o"`},
		{[]string{"reboot", "--json"}, true, `unknown op "reboot"`},
		{[]string{"status", "--json", "--verbose"}, true, "engine host status: flag provided but not defined: -verbose"},
		{[]string{"spawn", "--json", "--task", "x"}, true, `invalid value "x" for flag -task`},
		{[]string{"collect", "--json", "--run-id", "a", "b"}, true, "engine host collect: unexpected b"},
		{[]string{"collect", "--json", "--run-id", "none"}, true, "no run none on this host"},
		{[]string{"release", "--json", "--name", "r-7"}, false, "tmux died"},
	}
	for _, c := range cases {
		if c.args[0] == "release" {
			d.Tmux = (&tmuxFake{fail: errFake, windows: []string{}}).tmux()
		}
		code, reply, _ := call(t, d, "", c.args...)
		msg, _ := reply["error"].(string)
		if code != 1 || reply["refused"] != c.refused || !strings.Contains(msg, c.want) || reply["protocol"] != 1.0 {
			t.Errorf("%v: exit %d, reply %v; want refused %v and %q", c.args, code, reply, c.refused, c.want)
		}
	}
}

var errFake = &session.ExitError{Command: "tmux list-windows", Code: 1, Stderr: "tmux died"}

func TestUsageWithoutJSON(t *testing.T) {
	for _, args := range [][]string{nil, {"status"}, {"--json"}} {
		code, _, stderr := call(t, Deps{}, "", args...)
		if code != 2 || !strings.Contains(stderr, "usage: engine host <op> --json") {
			t.Errorf("%v: exit %d, stderr %q", args, code, stderr)
		}
	}
}

func TestSpawnReadsTheKickoffFromStdin(t *testing.T) {
	h := newHome(t, "mac")
	h.clone(t)
	tm := &tmuxFake{}
	d := Deps{Tmux: tm.tmux(), Home: h.dir, Run: (&gitFake{}).run, Getenv: func(string) string { return "" }, Now: fixedNow}
	args := []string{"spawn", "--json", "--repo", "o/r", "--task", "7", "--mode", "gated", "--model", "opus-x", "--effort", "high",
		"--run-id", "r-7-20260102T030405Z", "--tier", "deep", "--feature", ref(3)}
	code, reply, _ := call(t, d, "it's the kickoff\n", args...)
	if code != 0 || reply["window"] != "@9" || reply["name"] != "r-7" || !strings.Contains(tm.command, "s the kickoff") || strings.Contains(tm.command, "kickoff\n") {
		t.Errorf("exit %d, reply %v, command %s", code, reply, tm.command)
	}
	code, reply, _ = call(t, d, "", args...)
	if msg, _ := reply["error"].(string); code != 1 || !strings.Contains(msg, "the kickoff is empty") {
		t.Errorf("no kickoff: exit %d, reply %v", code, reply)
	}
}
