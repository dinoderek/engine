// Package start is `engine start --check`: the preflight that launches a
// throwaway session on a host to find prompts that would stop an unattended
// one. It reaches the host through a remote.Client: this host's, in process,
// unless Deps names another.
package start

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/remote"
	"github.com/dinoderek/engine/internal/session"
)

// Deps are the outside world: the CLIs, the home directory, the environment
// and the clock. Tests replace each one.
type Deps struct {
	// Run runs git and gh for engine's clone and the worktree.
	Run session.RunFunc
	// Probe reads a repo's own `dev` usage under a deadline; nil uses Run.
	Probe session.RunFunc
	Tmux  *session.Tmux
	Home  string
	// Getenv reads CLAUDE_CONFIG_DIR.
	Getenv func(string) string
	Now    func() time.Time
	// BaseURL is where repos are cloned from; GitHub when empty.
	BaseURL string
	// Sleep waits between the check's polls; nil is time.Sleep.
	Sleep func(time.Duration)
	// Exec runs the host's own CLIs, such as claude agents.
	Exec session.Runner
	// Client is the host the session starts on, for tests; nil is this host,
	// in process, through the fields above, or the named one over SSH.
	Client remote.Client
	// SSH runs ssh to reach a named host; nil starts real processes.
	SSH remote.Exec
}

// HostClient is the host the session starts on: host, reached over SSH when
// remote is set, else this host, in process.
func (d Deps) HostClient(host hosts.Host, remoteHost bool) remote.Client {
	switch {
	case d.Client != nil:
		return d.Client
	case remoteHost:
		run := d.SSH
		if run == nil {
			run = remote.ExecCommand
		}
		return remote.SSH{Name: host.Name, Address: host.Address, Exec: run}
	}
	return remote.Local{Deps: hostops.Deps{
		Run: d.Run, Probe: d.Probe, Exec: d.Exec, Tmux: d.Tmux, Home: d.Home, Getenv: d.Getenv, Now: d.Now, BaseURL: d.BaseURL, Sleep: d.Sleep,
		JoinRemote: true,
	}}
}

// pickHost returns the machine named name, and whether it is another one.
// An empty name, or this host's own, is this host.
func pickHost(cfg *hosts.Config, local hosts.Host, name string) (hosts.Host, bool, error) {
	if name == "" || name == local.Name {
		return local, false, nil
	}
	host, ok := cfg.Hosts[name]
	if !ok {
		return hosts.Host{}, false, refuse("host %q is not in hosts.toml; its machines are %s", name, strings.Join(cfg.HostNames(), ", "))
	}
	return host, true, nil
}

// RefusedError is a check that failed before the session started.
type RefusedError struct{ Reason string }

func (e *RefusedError) Error() string { return e.Reason + "; nothing was changed" }

func refuse(format string, args ...any) error {
	return &RefusedError{Reason: fmt.Sprintf(format, args...)}
}

// Check runs the preflight check of harness (Claude when empty) on host,
// which is this one when empty, prints its report, and reports whether an
// unattended session there would run without a prompt.
func Check(d Deps, host, harness string, out io.Writer) (bool, error) {
	if host == "" {
		return d.HostClient(hosts.Host{}, false).Check(harness, out)
	}
	cfg, local, err := hosts.Open(hosts.ConfigDir(d.Home))
	if err != nil {
		return false, refuse("%v", err)
	}
	target, isRemote, err := pickHost(cfg, local, host)
	if err != nil {
		return false, err
	}
	return d.HostClient(target, isRemote).Check(harness, out)
}
