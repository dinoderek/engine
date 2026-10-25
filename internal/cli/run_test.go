package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/remote"
	"github.com/dinoderek/engine/internal/tracker"
)

func TestRunCommandLine(t *testing.T) {
	token := func(k string) string { return map[string]string{"GH_TOKEN": "tok"}[k] }
	none := func(string) string { return "" }
	cases := []struct {
		name   string
		args   []string
		getenv func(string) string
		code   int
		stderr string
	}{
		{"usage", []string{"run"}, token, 2, "usage: engine run"},
		{"bad ref", []string{"run", "nope"}, token, 2, "want owner/repo#n"},
		{"no token", []string{"run", feature}, none, 1, "engine run: no GH_TOKEN"},
		{"no host config", []string{"run", feature}, token, 1, "engine run:"},
	}
	for _, c := range cases {
		var out, errOut bytes.Buffer
		env := Env{Out: &out, Err: &errOut, Getenv: c.getenv, Home: t.TempDir()}
		if code := runRun(c.args[1:], env, func(time.Duration) { t.Error("slept") }); code != c.code || !strings.Contains(errOut.String(), c.stderr) {
			t.Errorf("%s: exit %d, stderr %q", c.name, code, errOut.String())
		}
	}
}

func TestRunReachesThisHostInProcessAndOthersOverSSH(t *testing.T) {
	e := newPsEnv(t, "mac")
	gh := func(_ []string, _ string, args ...string) (string, error) {
		if strings.HasSuffix(args[1], "/blocked_by") {
			return `[]`, nil
		}
		return `{"number":1,"title":"F","state":"open","repository_url":"https://api.github.com/repos/o/r","labels":[{"name":"engine:feature"}]}`, nil
	}
	env := Env{Out: &e.out, Err: &e.errOut, Exec: gh, Local: e.local, Home: e.home, Getenv: func(string) string { return "" }}
	sys, taskOnly, err := runSystem(tracker.Ref{Repo: "o/r", Number: 1}, env)
	if err != nil || taskOnly {
		t.Fatalf("task only %v, %v", taskOnly, err)
	}
	if _, ok := sys.Client(sys.Config.Hosts["mac"]).(remote.Local); !ok {
		t.Errorf("this host is not reached in process: %T", sys.Client(sys.Config.Hosts["mac"]))
	}
	if c, ok := sys.Client(sys.Config.Hosts["devbox"]).(remote.SSH); !ok || c.Address != "me@devbox" || c.Name != "devbox" {
		t.Errorf("the devbox is not reached over SSH: %+v", sys.Client(sys.Config.Hosts["devbox"]))
	}
}
