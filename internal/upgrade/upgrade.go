// Package upgrade is `engine upgrade [--dry-run]`: it brings every host in
// hosts.toml, the caller included, to the commit at the head of main on
// GitHub. It is a fleet loop around `engine setup`, which does each host's
// work; what it adds is the target commit, the caller's own update, and the
// server's stop, backup and start through the systemd unit hosts.toml
// records for the server's host.
//
// The run, in order: a preflight and the target, which change nothing; the
// server's stop and backup, with the binary the server ran; the caller's
// engine and plugins; setup on the server's host with the new binary; the
// server's start; then setup on every other host. The start runs on every
// exit once the stop has been tried. A host that cannot be reached, or whose
// setup stays open, does not stop the others.
package upgrade

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/remote"
	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/setup"
)

// Task is one of the server's tasks, as the preflight reads it.
type Task struct{ ID, State string }

// Deps are the outside world. Tests replace each one.
type Deps struct {
	Home string
	// Exec runs ssh, this host's sh, and the new engine's setup.
	Exec remote.Exec
	// GH runs gh, to read the head of main and its go.mod.
	GH session.RunFunc
	// Harnesses are the adapters; the caller's plugin update runs those its
	// host lists.
	Harnesses harness.Adapters
	// Tasks lists the server's tasks. An error is a server that does not
	// answer.
	Tasks func() ([]Task, error)
	// Now stamps the backup's file name; Sleep paces the wait for the
	// server to answer after its start.
	Now   func() time.Time
	Sleep func(time.Duration)
	// Context ends the run early, as an interrupt does: no further step
	// runs, except the server's start.
	Context context.Context
}

// busy are the task states that refuse an upgrade: a session runs, a PR
// waits on the merge queue, or the operator is asked.
var busy = map[string]bool{"running": true, "in-review": true, "needs-human": true}

// answerTries and answerPause bound the wait for the server to answer after
// its start: about half a minute.
const (
	answerTries = 30
	answerPause = time.Second
)

// newBinary is where `go install` puts the engine, relative to the home.
const newBinary = ".local/bin/engine"

// refuse is an upgrade that stopped before changing anything.
func refuse(format string, args ...any) error {
	return fmt.Errorf(format+"; nothing was changed", args...)
}

// Run upgrades the fleet, or with dryRun prints the steps it would take, and
// reports whether every item is closed. A preflight that refuses is an
// error, and nothing was changed.
func Run(dryRun bool, d Deps, out io.Writer) (bool, error) {
	if d.Context == nil {
		d.Context = context.Background()
	}
	f, err := survey(d)
	if err != nil {
		return false, err
	}
	if err := f.resolveTarget(); err != nil {
		return false, err
	}
	f.actions = f.plan()
	if dryRun {
		f.printPlan(out)
		return true, nil
	}
	f.execute(out)
	return f.report(out), nil
}

// fleet is one upgrade: the hosts, what the preflight found, and what each
// step left.
type fleet struct {
	d      Deps
	caller hosts.Host
	server hosts.Host
	// hosts are every host, in name order; results is each one's line in
	// the report.
	hosts   []hosts.Host
	results map[string]*hostResult
	// active is what is-active said about the server's unit.
	active string
	// commit and toolchain are the target: main's head and its go.mod's Go.
	commit, toolchain string
	// stamp names the backup: the run's start, in UTC.
	stamp   string
	actions []action
	// opens are the items outside any host's setup still open: a failed
	// stop, backup, install, plugin update or start.
	opens []openItem
	// stopped is set once the stop has been tried, started once the start
	// has run; backedUp once the backup is written, answered once the
	// started server answers.
	stopped, started, backedUp, answered bool
}

// hostResult is one host's line in the report.
type hostResult struct {
	before, after, plugin string
	// unreachable is why the preflight could not reach the host.
	unreachable string
	// ran is set once its setup ran; open says why it is still open.
	ran  bool
	open string
}

// openItem is an item for the operator, closed by command.
type openItem struct{ detail, command string }

func (f *fleet) newBinary() string { return filepath.Join(f.d.Home, newBinary) }

// local reports whether h is the caller.
func (f *fleet) local(h hosts.Host) bool { return h.Name == f.caller.Name }

// shell is h's shell: this host's sh for the caller, else ssh.
func (f *fleet) shell(h hosts.Host) setup.Shell {
	if f.local(h) {
		return setup.NewShell(h.Name, "", f.d.Exec)
	}
	return setup.NewShell(h.Name, h.Address, f.d.Exec)
}

// where names h in the steps: "here" for the caller.
func (f *fleet) where(h hosts.Host) string {
	if f.local(h) {
		return "here"
	}
	return "on " + h.Name
}

func (f *fleet) interrupted() bool { return f.d.Context.Err() != nil }
