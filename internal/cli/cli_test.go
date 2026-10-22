package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/tracker"
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
		{"unknown", []string{"frobnicate"}, "", none, false, 2, "", `unknown command "frobnicate"`},
		{"labels without sync", []string{"labels", "o/r"}, "", none, false, 2, "", "usage: engine labels sync <owner/repo>"},
		{"labels sync without repo", []string{"labels", "sync"}, "", none, false, 2, "", "usage: engine labels sync <owner/repo>"},
		{"labels sync", []string{"labels", "sync", "o/r"}, "", token, false, 0, "13 engine labels in o/r", ""},
		{"labels sync, gh fails", []string{"labels", "sync", "o/r"}, "", token, true, 1, "", "HTTP 403"},
		{"labels sync, no token", []string{"labels", "sync", "o/r"}, "", none, false, 1, "", "no GH_TOKEN and no token file"},
		{"hook blocks", []string{"hook", "pre-tool-use"}, `{"tool_name":"Bash","tool_input":{"command":"gh pr merge --auto"}}`, none, false, 2, "", "merge guard"},
		{"hook allows", []string{"hook", "pre-tool-use"}, `{"tool_name":"Read","tool_input":{}}`, none, false, 0, "", ""},
		{"unknown hook fails open", []string{"hook", "stop"}, "", none, false, 1, "", `unknown hook "stop"`},
		{"hook without a name fails open", []string{"hook"}, "", none, false, 1, "", "unknown hook"},
		{"attach without a task", []string{"attach"}, "", token, false, 2, "", "usage: engine attach <repo>-<task>"},
		{"attach with two names", []string{"attach", "a-1", "b-2"}, "", token, false, 2, "", "usage: engine attach <repo>-<task>"},
		{"attach an unclaimed task", []string{"attach", "nope-1"}, "", token, false, 1, "", "engine attach: no claimed task named nope-1"},
		{"attach without a token", []string{"attach", "nope-1"}, "", none, false, 1, "", "engine attach: no GH_TOKEN and no token file"},
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

func TestGh(t *testing.T) {
	token := func(k string) string { return map[string]string{"GH_TOKEN": "tok"}[k] }
	none := func(string) string { return "" }
	tests := []struct {
		name      string
		getenv    func(string) string
		code      int
		err       error
		wantCode  int
		wantRan   bool
		wantError string
	}{
		{"passes the token and the args", token, 0, nil, 0, true, ""},
		{"returns gh's exit code", token, 4, nil, 4, true, ""},
		{"reports gh that cannot run", token, 1, errors.New("no gh"), 1, true, "engine gh: no gh"},
		{"refuses without a token", none, 0, nil, 1, false, "engine gh: no GH_TOKEN and no token file"},
		{"reads the token file", nil, 0, nil, 0, true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			in := strings.NewReader("body")
			ran := false
			stream := func(env []string, got tracker.Streams, name string, args ...string) (int, error) {
				ran = true
				if strings.Join(env, " ") != "GH_TOKEN=tok" || name != "gh" || strings.Join(args, " ") != "pr view 7" {
					t.Errorf("ran %v %s %v", env, name, args)
				}
				if got.In != in || got.Out != &out || got.Err != &errOut {
					t.Error("streams not passed through")
				}
				return tt.code, tt.err
			}
			env := Env{In: in, Out: &out, Err: &errOut, Stream: stream, Getenv: tt.getenv, Home: ghHome(t, tt.getenv == nil)}
			if env.Getenv == nil {
				env.Getenv = none
			}
			if code := Run([]string{"gh", "pr", "view", "7"}, env); code != tt.wantCode {
				t.Errorf("exit code %d, want %d", code, tt.wantCode)
			}
			if ran != tt.wantRan {
				t.Errorf("gh ran: %v, want %v", ran, tt.wantRan)
			}
			checkGhStderr(t, errOut.String(), tt.wantError)
		})
	}
}

// checkGhStderr wants the expected message on stderr, and never the token.
func checkGhStderr(t *testing.T, stderr, want string) {
	t.Helper()
	if !strings.Contains(stderr, want) {
		t.Errorf("stderr %q lacks %q", stderr, want)
	}
	if strings.Contains(strings.ReplaceAll(stderr, "token", ""), "tok") {
		t.Errorf("stderr %q shows the token", stderr)
	}
}

// ghHome is a home directory, holding the token file "tok" when withToken.
func ghHome(t *testing.T, withToken bool) string {
	t.Helper()
	home := t.TempDir()
	if !withToken {
		return home
	}
	path := filepath.Join(home, tracker.TokenFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("tok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestSystem(t *testing.T) {
	env := System()
	if env.In == nil || env.Out == nil || env.Err == nil || env.Exec == nil || env.Stream == nil || env.Getenv == nil || env.Run == nil || env.Tmux == nil || env.Now == nil {
		t.Errorf("System left a field unset: %+v", env)
	}
}
