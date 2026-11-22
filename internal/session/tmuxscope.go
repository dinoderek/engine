package session

import (
	"fmt"
	"os"
	"runtime"
)

// ScopeCommand runs a command in its own transient systemd scope under the
// user's manager. A tmux server started through it lives in that scope, not
// in the cgroup of the process that started it, so stopping or restarting
// that process's unit, such as the engine server's, leaves the server and
// every window running.
var ScopeCommand = []string{"systemd-run", "--user", "--scope", "--quiet", "--collect", "--description=engine session tmux"}

// systemdDir exists exactly when systemd is the host's init (sd_booted).
const systemdDir = "/run/systemd/system"

// hostScope is the scope command for a host: ScopeCommand on a Linux host
// booted with systemd, none anywhere else.
func hostScope(goos string, stat func(string) (os.FileInfo, error)) []string {
	if goos != "linux" {
		return nil
	}
	if info, err := stat(systemdDir); err != nil || !info.IsDir() {
		return nil
	}
	return ScopeCommand
}

func defaultScope() []string { return hostScope(runtime.GOOS, os.Stat) }

// newSession runs args, a tmux new-session, inside t.Scope when it is set,
// so the server it may start has a scope of its own. A scoped start that
// leaves no session, because systemd-run is missing or refused, runs again
// without the scope: scopeErr says why, and the server it starts shares its
// starter's cgroup. A scoped start that failed because another process
// created the session meanwhile is that failure, for the caller's race
// handling, with no scopeErr.
func (t *Tmux) newSession(args ...string) (out string, scopeErr, err error) {
	if len(t.Scope) == 0 {
		out, err = t.tmux(args...)
		return out, nil, err
	}
	scoped := append(append(append([]string{}, t.Scope[1:]...), t.Bin), t.args(args)...)
	out, scopeErr = t.Run(t.Scope[0], scoped...)
	if scopeErr == nil {
		return out, nil, nil
	}
	if exists, herr := t.HasSession(); herr == nil && exists {
		return "", nil, scopeErr
	}
	out, err = t.tmux(args...)
	if err != nil {
		return "", scopeErr, fmt.Errorf("%w (in its own scope first: %w)", err, scopeErr)
	}
	return out, scopeErr, nil
}
