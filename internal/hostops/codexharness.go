package hostops

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/session"
)

// codex runs a session joined to engine's own remote-control daemon when it
// can, else embedded. A joined session runs in engine's Codex home, whose
// config.toml trusts its folder; an embedded one runs in the host's own,
// with engine's profile for the session, which the launch selects with -p,
// trusting its folder, so the user's config.toml is never edited. Either way
// it runs the hooks of engine's Codex plugin without Codex's per-hash hook
// trust, whose review prompt would stop an unattended launch.
type codex struct {
	d       Deps
	profile string
}

func (codex) Name() string { return Codex }

// home is the host's own Codex home, which an embedded session runs in.
func (c codex) home() string { return session.CodexHome(c.getenv, c.d.Home) }

// engineHome is engine's own Codex home, where engine setup runs engine's
// daemon.
func (c codex) engineHome() string {
	return filepath.Join(c.d.Home, filepath.FromSlash(hosts.CodexHomeDir))
}

func (c codex) engineConfig() string { return filepath.Join(c.engineHome(), "config.toml") }

// getenv is Deps.Getenv, or an empty environment when that is nil, as in a
// release whose caller set none.
func (c codex) getenv(k string) string {
	if c.d.Getenv == nil {
		return ""
	}
	return c.d.Getenv(k)
}

// refusedFlags are what a launch line may carry that Codex refuses with
// --remote. Engine's config.toml holds the writable roots instead.
var refusedFlags = []string{"sandbox_workspace_write.writable_roots", "--add-dir"}

// startDaemon names the command that starts engine's daemon.
const startDaemon = "`engine setup` on this host starts it with remote control on"

// Join joins engine's daemon when it runs in engine's Codex home with remote
// control on. It reads the daemon with `codex app-server daemon version` in
// that home, which starts nothing, and its process's arguments. `codex
// remote-control --json` is no read: with no daemon it serves in the
// foreground until stopped, and the state it reports is not one Codex
// home's.
func (c codex) Join(run session.Runner, bin, line string) (bool, string) {
	why := c.unjoinable(run, bin, line)
	if why == "" {
		return true, ""
	}
	return false, "the Codex session runs embedded, so the ChatGPT and Codex apps do not show it: " + why
}

// unjoinable is why the session cannot join engine's daemon, or empty.
func (c codex) unjoinable(run session.Runner, bin, line string) string {
	for _, flag := range refusedFlags {
		if strings.Contains(line, flag) {
			return "hosts.toml's launch line has " + flag + ", which a joined session refuses"
		}
	}
	home := c.engineHome()
	out, err := run("env", "CODEX_HOME="+home, bin, "app-server", "daemon", "version")
	if state := daemonState(out, err); state != "running" {
		return "engine's Codex daemon is " + state + "; " + startDaemon
	}
	args, err := run("ps", "-ww", "-eo", "args=")
	if err != nil {
		return "cannot read engine's Codex daemon's arguments (" + err.Error() + ")"
	}
	if !remoteControlOn(args, home) {
		return "engine's Codex daemon runs with remote control off; " + startDaemon
	}
	return ""
}

// daemonState is the status `codex app-server daemon version` printed:
// running when a daemon runs. A command that exits non-zero found none; one
// that cannot run, or prints no status, is unreadable.
func daemonState(out string, err error) string {
	var exit *session.ExitError
	if errors.As(err, &exit) {
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

func (c codex) Trust(dir, clone string, joined bool) error {
	var err error
	if joined {
		err = session.TrustCodexFolder(c.engineConfig(), dir)
	} else {
		err = session.WriteCodexProfile(c.home(), c.profile, dir, clone)
	}
	if err != nil {
		return fmt.Errorf("trust %s for Codex: %w", dir, err)
	}
	return nil
}

// Untrust deletes the session's profile and removes dir's trust from
// engine's config.toml, whichever Trust wrote.
func (c codex) Untrust(dir string) error {
	_, err := session.RemoveCodexProfile(c.home(), c.profile)
	if dir != "" {
		err = errors.Join(err, session.UntrustCodexFolder(c.engineConfig(), dir))
	}
	return err
}

func (c codex) Env(joined bool) string {
	if joined {
		return "export CODEX_HOME=" + hosts.ShellWord(c.engineHome()) + " && "
	}
	if dir := c.getenv("CODEX_HOME"); dir != "" {
		return "export CODEX_HOME=" + hosts.ShellWord(dir) + " && "
	}
	return "unset CODEX_HOME && "
}

// Args join the daemon over its default socket in engine's Codex home, or
// select the session's profile. A joined session names its folder with -C:
// without it, the daemon starts the thread in its own working directory.
func (c codex) Args(l launch) string {
	if l.joined {
		return " -C " + hosts.ShellWord(l.dir) + " --dangerously-bypass-hook-trust -c " + sessionVars(l) +
			" --remote unix:// -- " + hosts.ShellWord(l.kickoff)
	}
	return " -p " + hosts.ShellWord(c.profile) + " --dangerously-bypass-hook-trust -- " + hosts.ShellWord(l.kickoff)
}

// sessionVars is the shell for the -c value that gives a joined session's
// commands what the window exports: the agent runs in the daemon, whose
// environment has none of it. TMUX and TMUX_PANE, which the session closes
// its own window with, come from the window's shell as it starts. The token
// is not one: it is in the daemon's environment already.
func sessionVars(l launch) string {
	vars := []string{session.RunDirVar + "=" + session.TOMLString(l.runDir), session.HarnessVar + "=" + session.TOMLString(Codex)}
	if l.mode == HandsOff {
		vars = append(vars, session.UnattendedVar+`="1"`)
	}
	return hosts.ShellWord("shell_environment_policy.set={"+strings.Join(vars, ",")+`,TMUX="`) + `"$TMUX"` +
		hosts.ShellWord(`",TMUX_PANE="`) + `"$TMUX_PANE"` + hosts.ShellWord(`"}`)
}
