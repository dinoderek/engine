package hostops

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/session"
)

// daemonEnvironment refuses to join a process whose host environment setup
// has not recorded. Embedded sessions use the caller's environment instead.
// It returns the daemon's PID when it may be joined.
func (c codex) daemonEnvironment(run session.Runner) (pid, why string) {
	out, err := run("ps", "-ww", "-eo", "pid=,lstart=,args=")
	if err != nil {
		return "", "cannot identify engine's Codex daemon (" + err.Error() + ")"
	}
	instance := ""
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 7 && remoteControlOn(strings.Join(fields[6:], " "), c.engineHome()) {
			if instance != "" {
				return "", "engine's Codex daemon identity is ambiguous"
			}
			instance = strings.Join(fields[:6], " ")
		}
	}
	receipt, err := os.ReadFile(filepath.Join(c.engineHome(), "daemon-environment"))
	if err != nil || instance == "" || strings.TrimSpace(string(receipt)) != instance {
		return "", "engine's Codex daemon needs a host environment refresh; run `engine setup` on this host"
	}
	pid, _, _ = strings.Cut(instance, " ")
	return pid, ""
}

// disconnected is why engine's daemon, process pid, has no remote-control
// connection to the apps, or empty when it has one. It reads the daemon's
// own log in engine's Codex home: the arguments only say remote control was
// asked for.
func (c codex) disconnected(run session.Runner, pid string) string {
	n, err := strconv.Atoi(pid)
	if err != nil {
		return "its PID " + pid + " cannot be read"
	}
	etime, err := run("ps", "-o", "etime=", "-p", pid)
	if err != nil {
		return "its start cannot be read (" + err.Error() + ")"
	}
	elapsed, err := hosts.ParseElapsed(etime)
	if err != nil {
		return "its start cannot be read (" + err.Error() + ")"
	}
	out, err := run("sqlite3", "-readonly", "-json", filepath.Join(c.engineHome(), hosts.CodexLogs), hosts.CodexRemoteSQL(n, elapsed))
	if err != nil {
		return "its log cannot be read (" + err.Error() + ")"
	}
	return hosts.CodexDisconnected(out)
}
