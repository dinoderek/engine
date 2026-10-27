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
	e.write("hosts.toml", "harness_ratio = { claude = 4, codex = 1 }\n"+psHosts)
	gh := func(_ []string, _ string, args ...string) (string, error) {
		if strings.HasSuffix(args[1], "/blocked_by") {
			return `[]`, nil
		}
		return `{"number":1,"title":"F","state":"open","repository_url":"https://api.github.com/repos/o/r","labels":[{"name":"engine:feature"}]}`, nil
	}
	env := Env{Out: &e.out, Err: &e.errOut, Exec: gh, Local: e.local, Home: e.home, Getenv: func(string) string { return "" }}
	sys, taskOnly, err := runSystem(tracker.Ref{Repo: "o/r", Number: 1}, env, map[string]int{"codex": 1})
	if err != nil || taskOnly {
		t.Fatalf("task only %v, %v", taskOnly, err)
	}
	if sys.Config.HarnessRatio["codex"] != 1 || len(sys.Config.HarnessRatio) != 1 {
		t.Fatalf("override: %v", sys.Config.HarnessRatio)
	}
	if _, ok := sys.Client(sys.Config.Hosts["mac"]).(remote.Local); !ok {
		t.Errorf("this host is not reached in process: %T", sys.Client(sys.Config.Hosts["mac"]))
	}
	if c, ok := sys.Client(sys.Config.Hosts["devbox"]).(remote.SSH); !ok || c.Address != "me@devbox" || c.Name != "devbox" {
		t.Errorf("the devbox is not reached over SSH: %+v", sys.Client(sys.Config.Hosts["devbox"]))
	}
}

func TestRunRatioArguments(t *testing.T) {
	for _, args := range [][]string{
		{feature, "--harness-ratio", "claude=4,codex=0"},
		{"--harness-ratio=claude=4,codex=0", feature},
	} {
		ref, ratio, err := parseRun(args)
		if err != nil || ref.String() != feature || ratio["claude"] != 4 || ratio["codex"] != 0 {
			t.Errorf("%v: %v %v %v", args, ref, ratio, err)
		}
	}
	for _, args := range [][]string{
		{feature, "--harness-ratio"}, {feature, "--harness-ratio="}, {feature, "--harness-ratio", "claude=-1"},
		{feature, "--harness-ratio", "claude=1", "--harness-ratio", "codex=1"}, {feature, "--wat"}, {feature, feature},
	} {
		var out, stderr bytes.Buffer
		code := runRun(args, Env{Out: &out, Err: &stderr}, func(time.Duration) { t.Error("slept") })
		if code != 2 || !strings.Contains(stderr.String(), "usage: engine run") {
			t.Errorf("%v: %d %s", args, code, stderr.String())
		}
	}
}

func TestRunRefusesInvalidRatioBeforeReadingTracker(t *testing.T) {
	for _, ratio := range []map[string]int{{"missing": 1}, {"claude": 0, "codex": 0}} {
		e := newPsEnv(t, "mac")
		env := Env{Home: e.home, Getenv: func(string) string { return "" }, Exec: func([]string, string, ...string) (string, error) {
			t.Fatal("read tracker before validation")
			return "", nil
		}}
		_, _, err := runSystem(tracker.Ref{Repo: "o/r", Number: 1}, env, ratio)
		if err == nil || !strings.Contains(err.Error(), "harness_ratio") {
			t.Errorf("%v: %v", ratio, err)
		}
	}
}
