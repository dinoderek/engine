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

// harness is every step of a launch that differs between harnesses: how a
// session's folder is trusted, where the harness keeps its config, and what
// follows the host's launch line.
type harness interface {
	// Name is the harness's name in hosts.toml and run.json.
	Name() string
	// Trust makes dir trusted for the session, so no trust prompt blocks
	// its start.
	Trust(dir string) error
	// Untrust undoes what Trust left that belongs to this session alone.
	Untrust() error
	// Env is the shell that exports the harness's config location, ending
	// in " && ": the tmux server's environment may differ from the caller's.
	Env() string
	// Args follow the launch line, ending with the kickoff.
	Args(kickoff string) string
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

func (c claude) Trust(dir string) error {
	if err := session.TrustFolder(session.ClaudeConfig(c.d.Getenv, c.d.Home), dir); err != nil {
		return fmt.Errorf("trust %s for Claude Code: %w", dir, err)
	}
	return nil
}

// Untrust leaves the folder trusted: the trust is a key in a file Claude
// Code shares with every session.
func (claude) Untrust() error { return nil }

func (c claude) Env() string {
	if dir := c.d.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return "export CLAUDE_CONFIG_DIR=" + hosts.ShellWord(dir) + " && "
	}
	return "unset CLAUDE_CONFIG_DIR && "
}

func (c claude) Args(kickoff string) string {
	args := " --add-dir " + hosts.ShellWord(session.PluginDir(c.d.Getenv, c.d.Home)) + " --allowedTools"
	for _, rule := range tmpReads {
		args += " " + hosts.ShellWord(rule)
	}
	return args + " -- " + hosts.ShellWord(kickoff)
}

// codex trusts the folder in engine's own profile for the session, which
// the launch selects with -p, so the user's config.toml is never edited. It
// runs the hooks of engine's Codex plugin without Codex's per-hash hook
// trust, whose review prompt would stop an unattended launch.
type codex struct {
	d       Deps
	profile string
}

func (codex) Name() string { return Codex }

func (c codex) home() string { return session.CodexHome(c.getenv, c.d.Home) }

// getenv is Deps.Getenv, or an empty environment when that is nil, as in a
// release whose caller set none.
func (c codex) getenv(k string) string {
	if c.d.Getenv == nil {
		return ""
	}
	return c.d.Getenv(k)
}

func (c codex) Trust(dir string) error {
	if err := session.WriteCodexProfile(c.home(), c.profile, dir); err != nil {
		return fmt.Errorf("trust %s for Codex: %w", dir, err)
	}
	return nil
}

func (c codex) Untrust() error {
	_, err := session.RemoveCodexProfile(c.home(), c.profile)
	return err
}

func (c codex) Env() string {
	if dir := c.getenv("CODEX_HOME"); dir != "" {
		return "export CODEX_HOME=" + hosts.ShellWord(dir) + " && "
	}
	return "unset CODEX_HOME && "
}

func (c codex) Args(kickoff string) string {
	return " -p " + hosts.ShellWord(c.profile) + " --dangerously-bypass-hook-trust -- " + hosts.ShellWord(kickoff)
}

// launch is what a window runs: the expanded launch line, the run directory,
// the mode and the kickoff.
type launch struct{ line, runDir, mode, kickoff string }

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
	script += h.Env() + "exec " + l.line + h.Args(l.kickoff)
	return "exec /bin/sh -c " + hosts.ShellWord(script)
}
