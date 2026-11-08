package hostops

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gh401 is what gh auth status prints when the keychain's token cannot be
// read: a session started over ssh on macOS.
const gh401 = `github.com
  X Failed to log in to github.com account octo (keyring)
  - Active account: true
  - The token in keyring is invalid.
  - To re-authenticate, run: gh auth login -h github.com
`

func TestCheckFailsWhenGhCannotAuthenticateInTheSession(t *testing.T) {
	f := &checkTmux{openPolls: 1, ghCode: 1, ghOut: gh401}
	d, _, _ := checkDeps(t, f)
	// The session itself does every step: only the probe sees the 401.
	f.act = func() { write(t, filepath.Join(checkTmp, "engine-"+checkRun), "ok\ndone\n", 0o600) }
	var out bytes.Buffer
	ok, err := RunCheck(d, "", &out)
	if ok || err != nil || !f.probed || !f.started {
		t.Fatalf("ok %v, err %v, probed %v, session started %v\n%s", ok, err, f.probed, f.started, out.String())
	}
	report := out.String()
	for _, want := range []string{"FAIL gh can reach GitHub from the session engine", "X Failed to log in to github.com account octo (keyring)",
		GHRemedy, "plaintext", "ok   the session did every step", "would stop"} {
		if !strings.Contains(report, want) {
			t.Errorf("report lacks %q:\n%s", want, report)
		}
	}
}

func TestCheckPassesTheGhProbe(t *testing.T) {
	f := &checkTmux{openPolls: 1}
	d, _, _ := checkDeps(t, f)
	f.act = func() { write(t, filepath.Join(checkTmp, "engine-"+checkRun), "ok\ndone\n", 0o600) }
	var out bytes.Buffer
	if ok, err := RunCheck(d, "", &out); !ok || err != nil || !strings.Contains(out.String(), "ok   gh can reach GitHub") ||
		strings.Contains(out.String(), GHRemedy) {
		t.Errorf("ok %v, err %v:\n%s", ok, err, out.String())
	}
}

func TestReportGHNamesTheFixForItsFailure(t *testing.T) {
	var keychain, signedOut bytes.Buffer
	reportGH(&keychain, map[bool]string{true: "ok  ", false: "FAIL"}, GHAuth{Output: gh401})
	reportGH(&signedOut, map[bool]string{true: "ok  ", false: "FAIL"}, GHAuth{Output: "You are not logged into any GitHub hosts."})
	if !strings.Contains(keychain.String(), GHRemedy) || strings.Contains(signedOut.String(), GHRemedy) ||
		!strings.Contains(signedOut.String(), "gh auth login -h github.com") {
		t.Errorf("keychain:\n%s\nsigned out:\n%s", keychain.String(), signedOut.String())
	}
}

func TestCheckKillsAStuckGhProbe(t *testing.T) {
	f := &checkTmux{openPolls: 1, ghStuck: true}
	d, _, _ := checkDeps(t, f)
	f.act = func() { write(t, filepath.Join(checkTmp, "engine-"+checkRun), "ok\ndone\n", 0o600) }
	r, err := Check(d, "", &bytes.Buffer{})
	if err != nil || r.OK() || r.GH.OK || !f.killedGH || !strings.Contains(r.GH.Output, "did not finish within 30s") {
		t.Errorf("result %+v, err %v, killed %v", r, err, f.killedGH)
	}
}

func TestCheckRefusesALeftoverGhProbe(t *testing.T) {
	f := &checkTmux{}
	d, _, _ := checkDeps(t, f)
	d.Tmux.Run = func(_ string, args ...string) (string, error) {
		if args[0] == "list-windows" {
			return "@8 %8 0 " + GHProbeName + "\n", nil
		}
		return "", nil
	}
	if _, err := Check(d, "", &bytes.Buffer{}); !isRefusal(err) || !strings.Contains(err.Error(), "already has a window "+GHProbeName) {
		t.Errorf("got %v", err)
	}
}

func TestReadGHProbe(t *testing.T) {
	cases := []struct {
		name, out, exit string // exit "-" is no file
		ok              bool
		output          string
	}{
		{"signed in", "✓ Logged in\n", "0\n", true, "✓ Logged in"},
		{"a 401", gh401, "1\n", false, "The token in keyring is invalid."},
		{"silent failure", "", "4\n", false, "gh auth status exited 4"},
		{"never finished", "", "-", false, "did not finish"},
		{"garbled exit code", "", "x\n", false, `unreadable exit code "x"`},
	}
	for _, c := range cases {
		dir := t.TempDir()
		write(t, filepath.Join(dir, ghAuthOut), c.out, 0o600)
		if c.exit != "-" {
			write(t, filepath.Join(dir, ghAuthExit), c.exit, 0o600)
		}
		got := readGHProbe(filepath.Join(dir, ghAuthOut), filepath.Join(dir, ghAuthExit))
		if got.OK != c.ok || !strings.Contains(got.Output, c.output) {
			t.Errorf("%s: %+v", c.name, got)
		}
	}
}

// The probe's shell line, for real: a window of a private tmux server runs a
// fake gh, and the probe reads back its output and exit code.
func TestGhProbeRunsInARealTmuxWindow(t *testing.T) {
	tm := privateTmux(t)
	bin := t.TempDir()
	gh := filepath.Join(bin, "gh")
	write(t, gh, "#!/bin/sh\n[ \"$*\" = 'auth status -h github.com' ] || exit 9\nprintf '%s' '"+gh401+"' >&2\nexit 1\n", 0o755)
	old := ghBin
	ghBin = gh
	t.Cleanup(func() { ghBin = old })
	runDir, work := t.TempDir(), t.TempDir()
	d := Deps{Tmux: tm, Sleep: func(time.Duration) { time.Sleep(10 * time.Millisecond) }}
	got, err := probeGH(d, runDir, work)
	if err != nil || got.OK || !strings.Contains(got.Output, "The token in keyring is invalid.") {
		t.Fatalf("probe %+v, %v", got, err)
	}
	if ok, err := tm.HasWindow(GHProbeName); ok || err != nil {
		t.Errorf("the probe's window is left: %v, %v", ok, err)
	}
	if _, err := os.Stat(filepath.Join(runDir, ghAuthExit)); err != nil {
		t.Errorf("no exit code in the run directory: %v", err)
	}
}
