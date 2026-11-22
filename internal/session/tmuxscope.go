package session

import (
	"fmt"
	"os"
	"runtime"
	"slices"
	"strings"
)

// ScopeCommand runs a command in its own transient systemd scope under the
// user's manager. A tmux server started through it lives in that scope, not
// in the cgroup of the process that started it, so stopping or restarting
// that process's unit, such as the engine server's, leaves the server and
// every window running.
var ScopeCommand = []string{"systemd-run", "--user", "--scope", "--quiet", "--collect", "--description=engine session tmux"}

// systemdDir exists exactly when systemd is the host's init (sd_booted).
const systemdDir = "/run/systemd/system"

// hostScope is the scope command for this process: ScopeCommand on a Linux
// host booted with systemd when this process runs inside a systemd service,
// whose stop would kill a tmux server it starts. Anywhere else, such as an
// SSH login's session scope, none: a scope under the user's manager would
// die with that manager when the last login ends on a host without linger,
// which a plain start survives. A cgroup that cannot be read is none.
func hostScope(goos string, stat func(string) (os.FileInfo, error), cgroup func() (string, error)) []string {
	if goos != "linux" {
		return nil
	}
	if info, err := stat(systemdDir); err != nil || !info.IsDir() {
		return nil
	}
	if self, err := cgroup(); err != nil || ServiceUnit(self) == "" {
		return nil
	}
	return ScopeCommand
}

func defaultScope() []string { return hostScope(runtime.GOOS, os.Stat, ownCgroup) }

// ownCgroup is this process's /proc/self/cgroup.
func ownCgroup() (string, error) {
	data, err := os.ReadFile("/proc/self/cgroup")
	return string(data), err
}

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

// ServiceUnit is the systemd service whose cgroup holds a process, read from
// its /proc/<pid>/cgroup: the unified hierarchy's line (`0::`), else the
// systemd one of a legacy host (`name=systemd`). The innermost unit on the
// path decides: a service is returned, a scope is none. A process outside
// systemd's hierarchy is in none.
func ServiceUnit(cgroup string) string {
	path, ok := "", false
	for _, line := range strings.Split(cgroup, "\n") {
		f := strings.SplitN(strings.TrimSpace(line), ":", 3)
		if len(f) != 3 {
			continue
		}
		if f[0] == "0" && f[1] == "" {
			path, ok = f[2], true
			break
		}
		if f[1] == "name=systemd" {
			path, ok = f[2], true
		}
	}
	if !ok {
		return ""
	}
	parts := strings.Split(path, "/")
	for _, p := range slices.Backward(parts) {
		switch {
		case strings.HasSuffix(p, ".service"):
			return p
		case strings.HasSuffix(p, ".scope"):
			return ""
		}
	}
	return ""
}
