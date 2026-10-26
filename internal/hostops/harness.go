package hostops

import (
	"fmt"

	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/session"
)

// The harnesses the ops launch. A request that names none launches Claude.
const (
	Claude = "claude"
	Codex  = "codex"
)

// tmpReads lets a Claude executor read back what it or its tests wrote to
// /tmp, such as screenshots, without a prompt. macOS resolves /tmp to
// /private/tmp.
var tmpReads = []string{"Read(//tmp/**)", "Read(//private/tmp/**)"}

// harness is every step of a launch that differs between harnesses: whether
// the session joins a remote-control daemon, how its folder is trusted, where
// the harness keeps its config, and what follows the host's launch line.
type harness interface {
	// Name is the harness's name in hosts.toml and run.json.
	Name() string
	// Join decides, through the harness binary bin, whether a session
	// launched with the expanded launch line joins the host's remote-control
	// daemon. It only reads. When the session starts without, warning says
	// why.
	Join(run session.Runner, bin, line string) (joined bool, warning string)
	// Trust makes dir, a folder in clone, trusted for the session, so no
	// trust prompt blocks its start.
	Trust(dir, clone string, joined bool) error
	// Untrust undoes what Trust left for dir that belongs to this session
	// alone. An empty dir undoes only what needs no folder.
	Untrust(dir string) error
	// Env is the shell that exports the harness's config location, ending
	// in " && ": the tmux server's environment may differ from the caller's.
	Env(joined bool) string
	// Args follow the launch line, ending in the kickoff.
	Args(l launch) string
}

// harnessFor returns the harness called name, or Claude when name is empty,
// for the session called sessionName.
func harnessFor(d Deps, name, sessionName string) (harness, error) {
	switch name {
	case "", Claude:
		return claude{d}, nil
	case Codex:
		return codex{d: d, profile: session.CodexProfile(sessionName)}, nil
	}
	return nil, refuse("unknown harness %q: want %s or %s", name, Claude, Codex)
}

// claude trusts the folder in Claude Code's state file. Its file tools ask
// before reading outside the worktree, which would stop an unattended
// executor, so it adds the engine plugin's directory with --add-dir and allows
// reads in /tmp with --allowedTools. /tmp only gets a read rule: Claude loads
// skills from every --add-dir directory, and anyone on the host can write to
// /tmp. The kickoff comes last, after --, because both options take every
// argument up to the next option.
type claude struct{ d Deps }

func (claude) Name() string { return Claude }

// Join is false: Claude's launch line names the session for Remote Control.
func (claude) Join(session.Runner, string, string) (bool, string) { return false, "" }

func (c claude) Trust(dir, _ string, _ bool) error {
	if err := session.TrustFolder(session.ClaudeConfig(c.d.Getenv, c.d.Home), dir); err != nil {
		return fmt.Errorf("trust %s for Claude Code: %w", dir, err)
	}
	return nil
}

// Untrust leaves the folder trusted: the trust is a key in a file Claude
// Code shares with every session.
func (claude) Untrust(string) error { return nil }

func (c claude) Env(bool) string {
	if dir := c.d.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return "export CLAUDE_CONFIG_DIR=" + hosts.ShellWord(dir) + " && "
	}
	return "unset CLAUDE_CONFIG_DIR && "
}

func (c claude) Args(l launch) string {
	args := " --add-dir " + hosts.ShellWord(session.PluginDir(c.d.Getenv, c.d.Home)) + " --allowedTools"
	for _, rule := range tmpReads {
		args += " " + hosts.ShellWord(rule)
	}
	return args + " -- " + hosts.ShellWord(l.kickoff)
}

// launch is what a window runs: the expanded launch line, the session's
// folder, the run directory, the mode, the kickoff, and whether the session
// joins remote control.
type launch struct {
	line, dir, runDir, mode, kickoff string
	joined                           bool
}

// launchCommand is a window's shell command. It runs under /bin/sh, whatever
// tmux's default shell is: export the machine user's token from its file, so
// it never appears on a command line, the run directory, the harness and,
// hands-off, the unattended mark, and the harness's config location, then
// run the launch line and the harness's own arguments.
func launchCommand(d Deps, h harness, l launch) string {
	// An empty token would make gh fall back to the operator's own login.
	script := fmt.Sprintf(`GH_TOKEN="$(cat %s)" && [ -n "$GH_TOKEN" ] && export GH_TOKEN && `, hosts.ShellWord(tokenFile(d.Home)))
	// The hooks log to the run directory and, unattended, deny or log.
	script += "export " + session.RunDirVar + "=" + hosts.ShellWord(l.runDir) + " && "
	script += "export " + session.HarnessVar + "=" + h.Name() + " && "
	if l.mode == HandsOff {
		script += "export " + session.UnattendedVar + "=1 && "
	} else {
		script += "unset " + session.UnattendedVar + " && "
	}
	script += h.Env(l.joined) + "exec " + l.line + h.Args(l)
	return "exec /bin/sh -c " + hosts.ShellWord(script)
}
