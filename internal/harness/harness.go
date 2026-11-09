// Package harness is what engine asks of an agent harness, such as Claude
// Code or Codex: the Adapter interface and the neutral types it speaks. The
// core (the host ops, setup, hooks and the coordinator) knows harnesses only
// through it, and gets its adapters from the registry
// (internal/harness/registry), which is the only package that names one.
package harness

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/session"
)

// Env is the outside world an adapter reads and runs. Tests replace each
// part.
type Env struct {
	// Exec runs the host's own CLIs, such as the harness binary and ps.
	Exec session.Runner
	Home string
	// Getenv reads the caller's environment; nil reads every name as empty.
	Getenv func(string) string
	Now    func() time.Time
}

// Var reads name from the environment, or "" when Getenv is nil.
func (e Env) Var(name string) string {
	if e.Getenv == nil {
		return ""
	}
	return e.Getenv(name)
}

// Time is Now, or the wall clock when Now is nil.
func (e Env) Time() time.Time {
	if e.Now == nil {
		return time.Now()
	}
	return e.Now()
}

// Adapter is everything engine does that differs between harnesses.
type Adapter interface {
	// Name is the harness's name as hosts.toml, run.json and ENGINE_HARNESS
	// spell it.
	Name() string
	// Session is the launch of the session called name. Files a launch
	// writes for this one session are named after it.
	Session(name string) Launcher
	// Observe reads, once per status, what the harness says on this host:
	// its quota, its sign-in and the state of each run in runs, the newest
	// run of every live window that names this harness, by window name. It
	// never fails: what it cannot read stays unknown, with the reason.
	Observe(ctx context.Context, host hosts.Host, runs map[string]session.Run) Snapshot
	// Hooks are the harness's hook events and replies, and its own entries.
	Hooks() Hooks
	// CheckKickoff is the preflight session's first message; marker is the
	// file the session writes and reads back.
	CheckKickoff(marker string) string
	// HookedState is true when the session's state comes from its hooks
	// alone, so a check whose session wrote no state file fails.
	HookedState() bool
}

// Launcher is one session's launch.
type Launcher interface {
	// Join decides, through the harness binary bin, whether a session
	// launched with the expanded launch line joins the host's remote-control
	// daemon. It only reads. When the session starts without, warning says
	// why; a joined session the apps cannot see yet is warned too.
	Join(bin, line string) (joined bool, warning string)
	// Trust makes dir, a folder in clone, trusted for the session, so no
	// trust prompt blocks its start.
	Trust(dir, clone string, joined bool) error
	// Release deletes the files that Trust and the launch wrote for this
	// session alone. An empty dir deletes only what needs no folder. It runs
	// for every adapter at release, whatever the run's harness: a spawn that
	// failed before run.json may have written them.
	Release(dir string) error
	// Env is the shell that exports the harness's config location, ending
	// in " && ": the tmux server's environment may differ from the caller's.
	Env(joined bool) string
	// Args follow the launch line, ending in the kickoff.
	Args(l Launch) string
}

// Launch is what a session's window runs.
type Launch struct {
	// Line is the host's launch line, expanded; Dir the session's folder;
	// RunDir its run directory.
	Line, Dir, RunDir string
	// Kickoff is the session's first message.
	Kickoff string
	// Unattended is a hands-off session, which nobody watches.
	Unattended bool
	// Joined is a session that joins the host's remote-control daemon.
	Joined bool
	// ServerURL, TokenFile and Attempt are the control server's, or empty
	// without one.
	ServerURL, TokenFile, Attempt string
}

// Snapshot is what one Observe read.
type Snapshot struct {
	// Quota is the account's newest reading, or nil when unknown.
	Quota *session.Quota
	// SignedOut is true only when the harness's own CLI says so.
	SignedOut bool
	// Sessions is the state of each run Observe was given, by window name;
	// a missing name is unknown.
	Sessions map[string]State
	// Error says why the sessions are unknown, when they all are.
	Error string
}

// State is a live window's state.
type State struct {
	// State is session.StateWorking, StateWaiting or StateLimited.
	State string
	// WaitingFor says what a waiting session needs, when the harness says.
	WaitingFor string
	// Background is a working session whose last turn has ended: only a
	// background job keeps it busy, and it takes typed input.
	Background bool
	// ResetsAt is a limited session's reset, in epoch seconds, or zero.
	ResetsAt int64
}

// Adapters is every adapter engine knows, by name.
type Adapters interface {
	// Get returns the adapter called name; "" is the default one.
	Get(name string) (Adapter, bool)
	// Names lists the adapters, the default first.
	Names() []string
}

// NewAdapters is the adapters given, the first being the default.
func NewAdapters(list ...Adapter) Adapters {
	return adapters(list)
}

type adapters []Adapter

func (a adapters) Get(name string) (Adapter, bool) {
	if name == "" && len(a) > 0 {
		return a[0], true
	}
	i := slices.IndexFunc(a, func(x Adapter) bool { return x.Name() == name })
	if i < 0 {
		return nil, false
	}
	return a[i], true
}

func (a adapters) Names() []string {
	names := make([]string, len(a))
	for i, x := range a {
		names[i] = x.Name()
	}
	return names
}

// Lookup returns the adapter called name, or an error naming the ones there
// are.
func Lookup(a Adapters, name string) (Adapter, error) {
	if a != nil {
		if x, ok := a.Get(name); ok {
			return x, nil
		}
	}
	var names []string
	if a != nil {
		names = a.Names()
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("unknown harness %q: no harness adapters", name)
	}
	return nil, fmt.Errorf("unknown harness %q: want %s", name, strings.Join(names, " or "))
}
