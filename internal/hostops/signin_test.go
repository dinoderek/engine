package hostops

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/session"
)

// signInFake answers claude agents with no sessions, and each harness's
// sign-in check with what the test gives it.
func signInFake(claude, codex func() (string, error)) session.Runner {
	return func(name string, args ...string) (string, error) {
		line := strings.Join(args, " ")
		switch {
		case line == "agents --json":
			return "[]", nil
		case line == "auth status":
			return claude()
		case name == "env" && strings.HasSuffix(line, "login status") && strings.HasPrefix(args[0], "CODEX_HOME="):
			return codex()
		}
		return "", errors.New("unexpected " + name + " " + line)
	}
}

func answer(out string, err error) func() (string, error) {
	return func() (string, error) { return out, err }
}

func exited(stderr string) error {
	return &session.ExitError{Command: "check", Code: 1, Stderr: stderr}
}

// Status lists a harness as signed out only when its own CLI says so. A
// check that fails to run, or answers in a way engine cannot read, leaves
// the harness unlisted: unknown is never signed out.
func TestStatusSignedOut(t *testing.T) {
	signedIn := answer(`{"loggedIn": true, "authMethod": "claude.ai"}`, nil)
	cases := []struct {
		name          string
		claude, codex func() (string, error)
		want          []string
	}{
		{"both signed in", signedIn, answer("Logged in using ChatGPT", nil), nil},
		{"claude signed out", answer(`{"loggedIn": false, "authMethod": "none"}`, exited("")), answer("Logged in", nil), []string{Claude}},
		{"codex signed out", signedIn, answer("", exited("Not logged in\n")), []string{Codex}},
		{"both signed out", answer(`{"loggedIn": false}`, exited("")), answer("Not logged in", exited("")), []string{Claude, Codex}},
		{"claude will not run", answer("", errors.New("exec: claude: not found")), answer("Logged in", nil), nil},
		{"claude unreadable", answer("Unknown command: auth", exited("")), answer("Logged in", nil), nil},
		{"claude silent on login", answer(`{"authMethod": "none"}`, exited("")), answer("Logged in", nil), nil},
		{"codex fails another way", signedIn, answer("", exited("error: unknown subcommand")), nil},
		{"codex will not run", signedIn, answer("", errors.New("exec: env: not found")), nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHome(t, "mac")
			tm := &tmuxFake{}
			got, err := ReadStatus(Deps{Tmux: tm.tmux(), Home: h.dir, Exec: signInFake(c.claude, c.codex)})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.SignedOut, c.want) {
				t.Errorf("signed out %v, want %v", got.SignedOut, c.want)
			}
		})
	}
}

// A host checks only the harnesses it runs.
func TestStatusChecksOnlyItsHarnesses(t *testing.T) {
	h := newHome(t, "box")
	var ran []string
	run := func(name string, args ...string) (string, error) {
		ran = append(ran, name+" "+strings.Join(args, " "))
		return "", exited("Not logged in")
	}
	got, err := ReadStatus(Deps{Tmux: (&tmuxFake{}).tmux(), Home: h.dir, Exec: run})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.SignedOut, []string{Codex}) || len(ran) != 1 || !strings.HasSuffix(ran[0], "login status") {
		t.Errorf("signed out %v after running %q", got.SignedOut, ran)
	}
}
