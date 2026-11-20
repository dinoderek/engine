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
	usageOut     = "usage.txt"
	readyStarted = "started"
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
func probeReady(d Deps, host hosts.Host, p readyProbe) (checks []ReadyCheck, noDoctor bool, err error) {
	path, err := host.PathPrefix(d.Home)
	if err != nil {
		return nil, false, refuse("%v", err)
	}
	wt := filepath.Join(p.dir, scratchDir)
	if err := p.repos.AddScratch(p.clone, wt, p.head); err != nil {
		return nil, false, err
	}
	// A failed removal is retried by the next probe's AddScratch.
	defer func() { _ = p.repos.RemoveScratch(p.clone, wt) }()
	for _, f := range []string{readyStarted, readyGHOut, readyGHExit, usageOut, doctorOut, doctorExit, doctorAbsent} {
		if err := os.Remove(filepath.Join(p.dir, f)); err != nil && !os.IsNotExist(err) {
			return nil, false, err
		}
	}
	name := readyWindow(p.clone.Repo)
	if err := closeWindows(d, name); err != nil {
		return nil, false, err
	}
	opened, err := d.Tmux.Open(session.Window{Name: name, Dir: wt, Command: "exec /bin/sh -c " + hosts.ShellWord(readyScript(p.dir, wt, path))})
	if err != nil {
		return nil, false, fmt.Errorf("open the readiness probe's window: %w", err)
	}
	gone, err := waitExited(d, opened.ID, ReadyTimeout)
	if !gone {
		_ = d.Tmux.Kill(opened.ID)
	}
	if err != nil {
		return nil, false, err
	}
	checks, noDoctor = readReady(p.dir)
	return checks, noDoctor, nil
}

// readyWindow is the tmux window of a repo's probe. A GitHub owner never
// holds two hyphens in a row, so "--" keeps every repo's name apart.
func readyWindow(repo session.Repo) string { return "engine-ready-" + repo.Owner + "--" + repo.Name }

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

// offersDoctor matches a usage line that lists doctor as a verb, alone or
// after ./dev, never a line that only mentions it, such as "There is no
// doctor verb".
const offersDoctor = `'^[[:space:]]*(\./dev[[:space:]]+)?doctor([[:space:]]|$)'`

// readyScript is the probe window's shell script. It marks its start once
// in the worktree, and each step writes its output and exit code to dir. A
// repo without an executable dev, or whose dev prints a usage that does not
// list doctor, leaves doctorAbsent instead; a dev that cannot print its
// usage, such as one whose toolchain is missing, still has its doctor run,
// which fails with the reason.
func readyScript(dir, wt string, path []string) string {
	file := func(name string) string { return hosts.ShellWord(filepath.Join(dir, name)) }
	usage := file(usageOut)
	return hosts.PathExport(path) + "cd " + hosts.ShellWord(wt) + " && { : > " + file(readyStarted) + "; " +
		ghStep(file(readyGHOut), file(readyGHExit)) + "; " +
		"if [ -x ./dev ]; then ./dev < /dev/null > " + usage + " 2>&1; " +
		"if grep -qiE '^[[:space:]]*usage' " + usage + " && ! grep -qE " + offersDoctor + " " + usage + "; then : > " + file(doctorAbsent) + "; " +
		"else ./dev doctor < /dev/null > " + file(doctorOut) + " 2>&1; echo $? > " + file(doctorExit) + "; fi; " +
		"else : > " + file(doctorAbsent) + "; fi; }"
}

// readReady reads the probe's step files: gh's check always, the doctor's
// once gh finished, unless the repo's dev offers none. It reports whether
// the repo offers no doctor. A probe that never started, or a gh that never
// finished, is that one failure.
func readReady(dir string) ([]ReadyCheck, bool) {
	if _, err := os.Stat(filepath.Join(dir, readyStarted)); err != nil {
		return []ReadyCheck{{Name: GHCheck, Output: "the probe's window ended before it entered its scratch worktree"}}, false
	}
	step := func(name, out, exit string) ReadyCheck {
		ok, output := readStep(filepath.Join(dir, out), filepath.Join(dir, exit), name, ReadyTimeout)
		return ReadyCheck{Name: name, OK: ok, Output: lastLines(output, readyLines)}
	}
	checks := []ReadyCheck{step(GHCheck, readyGHOut, readyGHExit)}
	if _, err := os.Stat(filepath.Join(dir, readyGHExit)); err != nil {
		return checks, false
	}
	if _, err := os.Stat(filepath.Join(dir, doctorAbsent)); err == nil {
		return checks, true
	}
	return append(checks, step(DoctorCheck, doctorOut, doctorExit)), false
}

// lastLines is the last n lines of s.
func lastLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}
