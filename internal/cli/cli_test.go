package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	gh := func(fail bool) func([]string, string, ...string) (string, error) {
		return func([]string, string, ...string) (string, error) {
			if fail {
				return "HTTP 403", errors.New("exit 1")
			}
			return "", nil
		}
	}
	token := func(k string) string { return map[string]string{"GH_TOKEN": "tok"}[k] }
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
		{"unknown", []string{"frobnicate"}, "", none, false, 2, "", `unknown command ["frobnicate"]`},
		{"labels without sync", []string{"labels", "o/r"}, "", none, false, 2, "", "unknown command"},
		{"labels sync", []string{"labels", "sync", "o/r"}, "", token, false, 0, "13 engine labels in o/r", ""},
		{"labels sync, gh fails", []string{"labels", "sync", "o/r"}, "", token, true, 1, "", "HTTP 403"},
		{"labels sync, no token", []string{"labels", "sync", "o/r"}, "", none, false, 1, "", "no GH_TOKEN and no token file"},
		{"hook blocks", []string{"hook", "pre-tool-use"}, `{"tool_name":"Bash","tool_input":{"command":"gh pr merge --auto"}}`, none, false, 2, "", "merge guard"},
		{"hook allows", []string{"hook", "pre-tool-use"}, `{"tool_name":"Read","tool_input":{}}`, none, false, 0, "", ""},
		{"unknown hook", []string{"hook", "stop"}, "", none, false, 2, "", "unknown command"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			env := Env{In: strings.NewReader(tt.stdin), Out: &out, Err: &errOut, Exec: gh(tt.fail), Getenv: tt.getenv, Home: t.TempDir()}
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
	if env.In == nil || env.Out == nil || env.Err == nil || env.Exec == nil || env.Getenv == nil {
		t.Errorf("System left a field unset: %+v", env)
	}
}
