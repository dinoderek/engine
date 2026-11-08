package cli

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// When GitHub refuses the host's sign-in, engine run and engine ps --github
// exit 1 with the same message, which says what to do.
func TestGitHubRefusingTheSignInEndsRunAndPs(t *testing.T) {
	const refused = `{"message":"Bad credentials","status":"401"}` + "\ngh: Bad credentials (HTTP 401)"
	want := "GitHub refuses the host's gh sign-in: gh: Bad credentials (HTTP 401). Check it with `gh auth status`, and sign in again with `gh auth login`\n"
	e := newPsEnv(t, "mac")
	e.ghErr, e.ghOut = errors.New("exit status 1"), refused
	if code := e.run(); code != 1 || e.errOut.String() != "engine ps: "+want {
		t.Errorf("engine ps --github: exit %d, stderr %q", code, e.errOut.String())
	}
	e.errOut.Reset()
	env := Env{Out: &e.out, Err: &e.errOut, Exec: e.gh, Local: e.local, Home: e.home, Getenv: func(string) string { return "" }}
	if code := runRun([]string{feature}, env, func(time.Duration) { t.Error("slept") }); code != 1 || e.errOut.String() != "engine run: "+want {
		t.Errorf("engine run: exit %d, stderr %q", code, e.errOut.String())
	}
	if strings.Contains(e.out.String(), "pass:") {
		t.Errorf("stdout %q", e.out.String())
	}
}
