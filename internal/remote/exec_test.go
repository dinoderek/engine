package remote

import (
	"errors"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/session"
)

func TestShellJoin(t *testing.T) {
	if got := ShellJoin([]string{"tmux", "it's", "#{x} y"}); got != `'tmux' 'it'\''s' '#{x} y'` {
		t.Errorf("got %s", got)
	}
}

// ExecCommand passes stdin and the streams, and reports an exit code as a
// code, not an error.
func TestExecCommand(t *testing.T) {
	var out, errOut strings.Builder
	code, err := ExecCommand(strings.NewReader("hello"), &out, &errOut, "sh", "-c", "cat; echo oops >&2; exit 3")
	if err != nil || code != 3 || out.String() != "hello" || errOut.String() != "oops\n" {
		t.Errorf("got code %d, err %v, out %q, stderr %q", code, err, out.String(), errOut.String())
	}
	if code, err := ExecCommand(nil, &out, &errOut, "sh", "-c", "exit 0"); err != nil || code != 0 {
		t.Errorf("success: %d, %v", code, err)
	}
}

func TestExecCommandMissingProgram(t *testing.T) {
	code, err := ExecCommand(nil, &strings.Builder{}, &strings.Builder{}, "engine-no-such-program")
	if err == nil || code != -1 || !strings.Contains(err.Error(), "engine-no-such-program") {
		t.Errorf("got %d, %v", code, err)
	}
}

// TmuxRunner sends the command as one quoted string, reads tmux's exit 1 as
// its own, and reports any other failure as ssh's.
func TestTmuxRunner(t *testing.T) {
	var argv []string
	exit := func(code int, stderr string) session.Runner {
		return func(name string, args ...string) (string, error) {
			argv = append([]string{name}, args...)
			return "out", &session.ExitError{Command: name, Code: code, Stderr: stderr}
		}
	}
	out, err := TmuxRunner(exit(1, ""), "me@box")("tmux", "has-session", "-t", "=a b")
	var got *session.ExitError
	if !errors.As(err, &got) || got.Code != 1 || out != "out" {
		t.Errorf("exit 1: %q, %v", out, err)
	}
	if want := []string{"ssh", "me@box", `'tmux' 'has-session' '-t' '=a b'`}; strings.Join(argv, "|") != strings.Join(want, "|") {
		t.Errorf("argv %q, want %q", argv, want)
	}
	for _, tc := range []struct {
		code         int
		stderr, want string
	}{
		{255, "no route\n", "ssh me@box: no route"},
		{127, "", "ssh me@box: exit status 127"},
	} {
		if _, err := TmuxRunner(exit(tc.code, tc.stderr), "me@box")("tmux", "x"); err == nil || err.Error() != tc.want {
			t.Errorf("exit %d: %v, want %q", tc.code, err, tc.want)
		}
	}
	ok := TmuxRunner(func(string, ...string) (string, error) { return "fine", nil }, "me@box")
	if out, err := ok("tmux", "x"); out != "fine" || err != nil {
		t.Errorf("success: %q, %v", out, err)
	}
}
