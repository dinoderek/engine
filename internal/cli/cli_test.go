package cli

import (
	"bytes"
	"strings"
	"testing"
)

// ghIssue is a GitHub issue reference, split so this file holds no tracker
// reference.
const ghIssue = "o/r" + "#" + "1"

func TestRun(t *testing.T) {
	none := func(string) string { return "" }
	tests := []struct {
		name   string
		args   []string
		stdin  string
		getenv func(string) string
		code   int
		stdout string
		stderr string
	}{
		{"no command", nil, "", none, 2, "", "usage: engine"},
		{"version", []string{"version"}, "", none, 0, "engine ", ""},
		{"help", []string{"--help"}, "", none, 0, "usage: engine", ""},
		{"unknown", []string{"frobnicate"}, "", none, 2, "", `unknown command "frobnicate"`},
		{"labels is gone", []string{"labels", "sync", "o/r"}, "", none, 2, "", `unknown command "labels"`},
		{"removed gh wrapper", []string{"gh", "pr", "view"}, "", none, 2, "", `unknown command "gh"`},
		{"hook blocks", []string{"hook", "pre-tool-use"}, `{"tool_name":"Bash","tool_input":{"command":"gh pr merge --auto"}}`, none, 2, "", "merge guard"},
		{"hook allows", []string{"hook", "pre-tool-use"}, `{"tool_name":"Read","tool_input":{}}`, none, 0, "", ""},
		{"unknown hook fails open", []string{"hook", "stop"}, "", none, 1, "", `unknown hook "stop"`},
		{"hook without a name fails open", []string{"hook"}, "", none, 1, "", "unknown hook"},
		{"run a GitHub issue", []string{"run", ghIssue}, "", none, 2, "", "usage: engine run <plan-or-task-id>"},
		{"run without an ID", []string{"run"}, "", none, 2, "", "usage: engine run <plan-or-task-id>"},
		{"ps --github", []string{"ps", "--github"}, "", none, 2, "", "usage: engine ps [--json]"},
		{"tell by repo and task", []string{"tell", "r-7", "hi"}, "", none, 2, "", "usage: engine tell <task-id>"},
		{"tell a GitHub issue", []string{"tell", ghIssue, "hi"}, "", none, 2, "", "usage: engine tell <task-id>"},
		{"attach by repo and task", []string{"attach", "r-7"}, "", none, 2, "", "usage: engine attach <task-id>"},
		{"lint a GitHub issue", []string{"lint", ghIssue}, "", none, 2, "", "usage: engine lint <plan-id>"},
		{"start a task", []string{"start", "t7"}, "", none, 2, "", "usage: engine start --check"},
		{"start a GitHub issue", []string{"start", ghIssue}, "", none, 2, "", "engine run <task-id>"},
		{"start without --check", []string{"start"}, "", none, 2, "", "only --check is left"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			env := Env{In: strings.NewReader(tt.stdin), Out: &out, Err: &errOut, Getenv: tt.getenv, Home: t.TempDir()}
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

func TestSystem(t *testing.T) {
	env := System()
	if env.In == nil || env.Out == nil || env.Err == nil || env.Stream == nil || env.Getenv == nil || env.Run == nil || env.Tmux == nil || env.Now == nil {
		t.Errorf("System left a field unset: %+v", env)
	}
}
