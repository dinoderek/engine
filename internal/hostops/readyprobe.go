package hostops

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/session"
)

// ReadyTimeout bounds a readiness probe's window: gh's check, the repo's
// usage and its doctor together. It stays under the coordinator's limit on a
// host command.
const ReadyTimeout = 5 * time.Minute

// readyLines is how much of a step's output a result keeps: the end, where
// a failure says why.
const readyLines = 40

// The probe's step files in the repo's readiness directory.
const (
	readyGHOut   = "gh-auth.txt"
	readyGHExit  = "gh-auth.exit"
	doctorOut    = "doctor.txt"
	doctorExit   = "doctor.exit"
	doctorAbsent = "doctor.absent"
	scratchDir   = "worktree"
)

// readyProbe is one probe: the repo's clone and how to reach it, the head
// to check out, and the readiness directory.
type readyProbe struct {
	repos session.Repos
	clone session.Clone
	head  string
	dir   string
}

// probeReady checks the head out in a scratch worktree of the clone and runs,
// in a window of the session engine with the host's path first on PATH, as a
// session's would be: gh's sign-in check, then the repo's `./dev doctor` if
// its `dev` usage lists doctor. The worktree is removed afterwards; one left
// by a probe that died is replaced by the next.
func probeReady(d Deps, host hosts.Host, p readyProbe) ([]ReadyCheck, error) {
	path, err := host.PathPrefix(d.Home)
	if err != nil {
		return nil, refuse("%v", err)
	}
	wt := filepath.Join(p.dir, scratchDir)
	if err := p.repos.AddScratch(p.clone, wt, p.head); err != nil {
		return nil, err
	}
	// A failed removal is retried by the next probe's AddScratch.
	defer func() { _ = p.repos.RemoveScratch(p.clone, wt) }()
	for _, f := range []string{readyGHOut, readyGHExit, doctorOut, doctorExit, doctorAbsent} {
		if err := os.Remove(filepath.Join(p.dir, f)); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	name := readyWindow(p.clone.Repo)
	if err := closeWindows(d, name); err != nil {
		return nil, err
	}
	opened, err := d.Tmux.Open(session.Window{Name: name, Dir: wt, Command: "exec /bin/sh -c " + hosts.ShellWord(readyScript(p.dir, wt, path))})
	if err != nil {
		return nil, fmt.Errorf("open the readiness probe's window: %w", err)
	}
	gone, err := waitExited(d, opened.ID, ReadyTimeout)
	if !gone {
		_ = d.Tmux.Kill(opened.ID)
	}
	if err != nil {
		return nil, err
	}
	return readReady(p.dir), nil
}

// readyWindow is the tmux window of a repo's probe.
func readyWindow(repo session.Repo) string { return "engine-ready-" + repo.Owner + "-" + repo.Name }

// closeWindows kills every window called name: under the readiness lock,
// one is a probe that died.
func closeWindows(d Deps, name string) error {
	windows, err := d.Tmux.Windows()
	if err != nil {
		return err
	}
	for _, w := range windows {
		if w.Name == name {
			if err := d.Tmux.Kill(w.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// readyScript is the probe window's shell script. Each step writes its
// output and exit code to dir; a `dev` whose usage does not list doctor
// leaves doctorAbsent instead.
func readyScript(dir, wt string, path []string) string {
	file := func(name string) string { return hosts.ShellWord(filepath.Join(dir, name)) }
	return hosts.PathExport(path) + "cd " + hosts.ShellWord(wt) + " && { " +
		ghStep(file(readyGHOut), file(readyGHExit)) + "; " +
		"if [ -x ./dev ] && ./dev < /dev/null 2>&1 | grep -qw doctor; then " +
		"./dev doctor < /dev/null > " + file(doctorOut) + " 2>&1; echo $? > " + file(doctorExit) + "; " +
		"else : > " + file(doctorAbsent) + "; fi; }"
}

// readReady reads the probe's step files: gh's check always, the doctor's
// unless the repo's dev offers none.
func readReady(dir string) []ReadyCheck {
	step := func(name, out, exit string) ReadyCheck {
		ok, output := readStep(filepath.Join(dir, out), filepath.Join(dir, exit), name, ReadyTimeout)
		return ReadyCheck{Name: name, OK: ok, Output: lastLines(output, readyLines)}
	}
	checks := []ReadyCheck{step(GHCheck, readyGHOut, readyGHExit)}
	if _, err := os.Stat(filepath.Join(dir, doctorAbsent)); err == nil {
		return checks
	}
	return append(checks, step(DoctorCheck, doctorOut, doctorExit))
}

// lastLines is the last n lines of s.
func lastLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}
