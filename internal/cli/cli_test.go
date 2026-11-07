package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/tracker"
)

// feature is a feature reference, split so this file holds no tracker
// reference.
const feature = "o/r" + "#" + "1"

func TestRun(t *testing.T) {
	gh := func(fail bool) func([]string, string, ...string) (string, error) {
		return func(env []string, name string, args ...string) (string, error) {
			if len(env) != 0 {
				t.Fatal("GitHub auth overridden")
			}
			if len(args) > 1 && args[1] == "search/issues" {
				return `{"items":[]}`, nil
			}
			if fail {
				return "HTTP 403", errors.New("exit 1")
			}
			return "developer", nil
		}
	}
	none := func(string) string { return "" }
	tests := []struct {
		name   string
		args   []string
		stdin  string
		getenv func(string) string
		fail   bool
		code   int
		stdout string
		stderr string
	}{
		{"no command", nil, "", none, false, 2, "", "usage: engine"},
		{"version", []string{"version"}, "", none, false, 0, "engine ", ""},
		{"help", []string{"--help"}, "", none, false, 0, "usage: engine", ""},
		{"unknown", []string{"frobnicate"}, "", none, false, 2, "", `unknown command "frobnicate"`},
		{"labels without sync", []string{"labels", "o/r"}, "", none, false, 2, "", "usage: engine labels sync <owner/repo>"},
		{"labels sync without repo", []string{"labels", "sync"}, "", none, false, 2, "", "usage: engine labels sync <owner/repo>"},
		{"labels sync", []string{"labels", "sync", "o/r"}, "", none, false, 0, "14 engine labels in o/r", ""},
		{"labels sync, gh fails", []string{"labels", "sync", "o/r"}, "", none, true, 1, "", "HTTP 403"},
		{"removed gh wrapper", []string{"gh", "pr", "view"}, "", none, false, 2, "", `unknown command "gh"`},
		{"hook blocks", []string{"hook", "pre-tool-use"}, `{"tool_name":"Bash","tool_input":{"command":"gh pr merge --auto"}}`, none, false, 2, "", "merge guard"},
		{"hook allows", []string{"hook", "pre-tool-use"}, `{"tool_name":"Read","tool_input":{}}`, none, false, 0, "", ""},
		{"unknown hook fails open", []string{"hook", "stop"}, "", none, false, 1, "", `unknown hook "stop"`},
		{"hook without a name fails open", []string{"hook"}, "", none, false, 1, "", "unknown hook"},
		{"tell without text", []string{"tell", "r-7"}, "", none, false, 2, "", "usage: engine tell"},
		{"tell with empty stdin", []string{"tell", "r-7", "-"}, "", none, false, 2, "", "engine tell: the text is empty"},
		{"tell with blank stdin", []string{"tell", "r-7", "-"}, " \n\n", none, false, 2, "", "engine tell: the text is empty"},
		{"tell an unclaimed task", []string{"tell", "nope-1", "hi"}, "", none, false, 1, "", "engine tell: no claimed task named nope-1"},
		{"lint without a feature", []string{"lint"}, "", none, false, 2, "", "usage: engine lint"},
		{"lint a bad ref", []string{"lint", "nope"}, "", none, false, 2, "", "want owner/repo#n"},
		{"lint, gh fails", []string{"lint", feature}, "", none, true, 1, "", "engine lint: read " + feature},
		{"attach without a task", []string{"attach"}, "", none, false, 2, "", "usage: engine attach <repo>-<task>"},
		{"attach with two names", []string{"attach", "a-1", "b-2"}, "", none, false, 2, "", "usage: engine attach <repo>-<task>"},
		{"attach an unclaimed task", []string{"attach", "nope-1"}, "", none, false, 1, "", "engine attach: no claimed task named nope-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			env := Env{In: strings.NewReader(tt.stdin), Out: &out, Err: &errOut, Exec: gh(tt.fail), Stream: noStream(t), Getenv: tt.getenv, Home: t.TempDir()}
			if code := Run(tt.args, env); code != tt.code {
				t.Errorf("exit code %d, want %d; stderr %s", code, tt.code, errOut.String())
			}
			if !strings.Contains(out.String(), tt.stdout) {
				t.Errorf("stdout %q lacks %q", out.String(), tt.stdout)
			}
			if !strings.Contains(errOut.String(), tt.stderr) {
				t.Errorf("stderr %q lacks %q", errOut.String(), tt.stderr)
			}
		})
	}
}

func noStream(t *testing.T) tracker.Streamer {
	return func([]string, tracker.Streams, string, ...string) (int, error) {
		t.Error("an unexpected streamed command ran")
		return 1, nil
	}
}

func TestSystem(t *testing.T) {
	env := System()
	if env.In == nil || env.Out == nil || env.Err == nil || env.Exec == nil || env.Stream == nil || env.Getenv == nil || env.Run == nil || env.Tmux == nil || env.Now == nil {
		t.Errorf("System left a field unset: %+v", env)
	}
}
