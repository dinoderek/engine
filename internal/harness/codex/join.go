package codex

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/session"
)

// refusedFlags are what a launch line may carry that Codex refuses with
// --remote. Engine's config.toml holds the writable roots instead.
var refusedFlags = []string{"sandbox_workspace_write.writable_roots", "--add-dir"}

// startDaemon names the command that starts engine's daemon.
const startDaemon = "`engine setup` on this host starts it with remote control on"

// Join joins engine's daemon when it runs in engine's Codex home with remote
// control on. It reads the daemon with `codex app-server daemon version` in
// that home, which starts nothing, and its process's arguments. A joined
// session is warned when the daemon's log shows no live connection to the
// apps (see disconnected). `codex remote-control --json` is no read: with no
// daemon it serves in the foreground until stopped, and the state it reports
// is not one Codex home's.
func (l launcher) Join(bin, line string) (bool, string) {
	c, run := l.Adapter, l.env.Exec
	pid, why := c.unjoinable(run, bin, line)
	if why != "" {
		return false, "the Codex session runs embedded, so the ChatGPT and Codex apps do not show it: " + why
	}
	return true, c.disconnected(run, pid)
}

// unjoinable is why the session cannot join engine's daemon, or empty with
// the daemon's PID.
func (c Adapter) unjoinable(run session.Runner, bin, line string) (pid, why string) {
	for _, flag := range refusedFlags {
		if strings.Contains(line, flag) {
			return "", "hosts.toml's launch line has " + flag + ", which a joined session refuses"
		}
	}
	home := c.engineHome()
	out, err := run("env", "CODEX_HOME="+home, bin, "app-server", "daemon", "version")
	if state := daemonState(out, err); state != "running" {
		return "", "engine's Codex daemon is " + state + "; " + startDaemon
	}
	args, err := run("ps", "-ww", "-eo", "args=")
	if err != nil {
		return "", "cannot read engine's Codex daemon's arguments (" + err.Error() + ")"
	}
	if !remoteControlOn(args, home) {
		return "", "engine's Codex daemon runs with remote control off; " + startDaemon
	}
	return c.daemonEnvironment(run)
}

// daemonState is the status `codex app-server daemon version` printed:
// running when a daemon runs. A command that exits non-zero found none, and
// the last line of its errors says why; one that cannot run, or prints no
// status, is unreadable.
func daemonState(out string, err error) string {
	var exit *session.ExitError
	if errors.As(err, &exit) {
		if lines := strings.Split(strings.TrimSpace(exit.Stderr), "\n"); lines[len(lines)-1] != "" {
			return "not running (" + strings.TrimSpace(lines[len(lines)-1]) + ")"
		}
		return "not running"
	}
	if err != nil {
		return "unreadable (" + err.Error() + ")"
	}
	var reply struct {
		Status string `json:"status"`
	}
	if json.Unmarshal([]byte(out), &reply) != nil || reply.Status == "" {
		return "unreadable"
	}
	return reply.Status
}

// remoteControlOn reports whether a process in ps's output, one argument
// list a line, runs from the Codex home's packages with remote control on.
// Only the program, the first field, is matched, so a command that merely
// names the path does not count.
func remoteControlOn(ps, home string) bool {
	prefix := home + "/packages/"
	for _, line := range strings.Split(ps, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 1 && strings.HasPrefix(fields[0], prefix) && slices.Contains(fields[1:], "--remote-control") {
			return true
		}
	}
	return false
}

// daemonEnvironment refuses to join a process whose host environment setup
// has not recorded. Embedded sessions use the caller's environment instead.
// It returns the daemon's PID when it may be joined.
func (c Adapter) daemonEnvironment(run session.Runner) (pid, why string) {
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

// disconnected is the warning for a session joined to engine's daemon,
// process pid, that the apps may not show, or empty when the daemon has a
// remote-control connection. It reads the daemon's own log in engine's Codex
// home: the arguments only say remote control was asked for. A log that
// cannot be read (sqlite3 missing, say) leaves the connection unknown.
func (c Adapter) disconnected(run session.Runner, pid string) string {
	const unknown = "the Codex session joined engine's daemon, but whether the ChatGPT and Codex apps show it is unknown: its remote-control connection cannot be read ("
	n, err := strconv.Atoi(pid)
	if err != nil {
		return unknown + "its PID " + pid + " cannot be read)"
	}
	etime, err := run("ps", "-o", "etime=", "-p", pid)
	if err != nil {
		return unknown + "its start cannot be read: " + err.Error() + ")"
	}
	elapsed, err := hosts.ParseElapsed(etime)
	if err != nil {
		return unknown + "its start cannot be read: " + err.Error() + ")"
	}
	out, err := run("sqlite3", "-readonly", "-json", filepath.Join(c.engineHome(), hosts.CodexLogs), hosts.CodexRemoteSQL(n, elapsed))
	if err != nil {
		return unknown + "its log cannot be read: " + err.Error() + ")"
	}
	why, known := hosts.CodexDisconnected(out)
	switch {
	case !known:
		return unknown + why + ")"
	case why != "":
		return "the Codex session joined engine's daemon, but the ChatGPT and Codex apps do not show it: its remote control is not connected (" + why + ")"
	}
	return ""
}
