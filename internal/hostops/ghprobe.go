package hostops

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/session"
)

// GHProbeName is the tmux window of the check's gh probe.
const GHProbeName = CheckName + "-gh"

// GHRemedy moves gh's token out of the macOS login keychain into gh's own
// config file, which a tmux server started over ssh can read. It must run
// where gh can still read the keychain: a Terminal on the host.
const GHRemedy = "gh auth token | gh auth login -h github.com --with-token --insecure-storage"

// The probe's files in the run directory: what gh said, and its exit code.
const (
	ghAuthOut  = "gh-auth.txt"
	ghAuthExit = "gh-auth.exit"
)

// ghBin is the gh the probe runs, found on the tmux server's PATH as a
// session's would be. Tests replace it.
var ghBin = "gh"

// ghProbeTimeout is how long the probe waits for gh; ghProbePoll is how often
// it looks.
const (
	ghProbeTimeout = 30 * time.Second
	ghProbePoll    = time.Second
)

// GHAuth is what `gh auth status` said in a window of the session engine,
// where a spawned session's gh runs.
type GHAuth struct {
	// OK is true when gh exited 0: it has a token it can read and GitHub
	// accepts.
	OK bool
	// Output is what gh printed, or why the probe has no answer.
	Output string
}

// probeGH runs `gh auth status -h github.com` in a throwaway window of the
// session engine, so it inherits the tmux server's context as a spawned
// session does, with path first on PATH, and reads its output and exit code
// from runDir. dir is the window's directory.
func probeGH(d Deps, runDir, dir string, path []string) (GHAuth, error) {
	out, exit := filepath.Join(runDir, ghAuthOut), filepath.Join(runDir, ghAuthExit)
	script := hosts.PathExport(path) + ghStep(out, exit)
	opened, err := d.Tmux.Open(session.Window{Name: GHProbeName, Dir: dir, Command: "exec /bin/sh -c " + hosts.ShellWord(script)})
	if err != nil {
		return GHAuth{}, fmt.Errorf("open the gh probe's window: %w", err)
	}
	gone, err := waitExited(d, opened.ID, ghProbeTimeout)
	if !gone {
		// A dead pane kept for reading, or a gh still running: the files hold
		// any answer, and a failure is the caller's to report.
		_ = d.Tmux.Kill(opened.ID)
	}
	if err != nil {
		return GHAuth{}, err
	}
	return readGHProbe(out, exit), nil
}

// ghStep is the shell command that runs gh's sign-in check with its output
// in out and its exit code in exit.
func ghStep(out, exit string) string {
	return hosts.ShellWord(ghBin) + " auth status -h github.com < /dev/null > " + hosts.ShellWord(out) + " 2>&1; echo $? > " + hosts.ShellWord(exit)
}

// waitExited polls, looking before it first sleeps, until the window with id
// has exited or is gone, or timeout. It reports whether the window is gone.
func waitExited(d Deps, id string, timeout time.Duration) (bool, error) {
	sleep := d.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	for waited := time.Duration(0); ; waited += ghProbePoll {
		windows, err := d.Tmux.Windows()
		if err != nil {
			return false, err
		}
		w, listed := find(windows, id)
		if !listed || w.Dead || waited >= timeout {
			return !listed, nil
		}
		sleep(ghProbePoll)
	}
}

// readGHProbe reads the probe's files. No exit code means gh did not finish.
func readGHProbe(outPath, exitPath string) GHAuth {
	ok, output := readStep(outPath, exitPath, GHCheck, ghProbeTimeout)
	return GHAuth{OK: ok, Output: output}
}

// readStep reads a probe step's output and exit code, written by a window
// that had timeout to run it: what names the step. No exit code means the
// step did not finish.
func readStep(outPath, exitPath, what string, timeout time.Duration) (bool, string) {
	text, _ := os.ReadFile(outPath)
	output := strings.TrimRight(string(text), "\n")
	codeText, err := os.ReadFile(exitPath)
	if err != nil {
		return false, strings.TrimLeft(output+"\n"+fmt.Sprintf("%s did not finish within %s", what, timeout), "\n")
	}
	code, err := strconv.Atoi(strings.TrimSpace(string(codeText)))
	if err != nil {
		return false, fmt.Sprintf("%s left an unreadable exit code %q", what, strings.TrimSpace(string(codeText)))
	}
	if code != 0 && output == "" {
		output = fmt.Sprintf("%s exited %d", what, code)
	}
	return code == 0, output
}

// reportGH prints the probe's finding, with gh's output and the remedy when
// it failed.
func reportGH(out io.Writer, mark map[bool]string, gh GHAuth) {
	fmt.Fprintf(out, "%s gh can reach GitHub from the session engine (gh auth status)\n", mark[gh.OK])
	if gh.OK {
		return
	}
	fmt.Fprintf(out, "%s\n", indent(gh.Output))
	if !strings.Contains(gh.Output, "keyring") {
		fmt.Fprint(out, "     every gh call a session here makes would fail; sign gh in on this host: gh auth login -h github.com\n")
		return
	}
	fmt.Fprint(out, "     every gh call a session here makes would fail. On macOS a tmux server started over ssh cannot\n"+
		"     read the login keychain, where gh keeps its token by default. In a Terminal on this host, run\n"+
		"       "+GHRemedy+"\n"+
		"     which stores the token in plaintext in gh's config file, then rerun the check\n")
}
