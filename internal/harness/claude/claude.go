// Package claude is the Claude Code adapter.
//
// A session is launched in auto mode, whose classifier approves or refuses
// most actions without a prompt. The launch line names the session for
// Remote Control (-n and --remote-control), so a session never joins a
// daemon. Claude Code shows a workspace trust dialog in a new folder, whose
// default answer exits, so Trust records the folder as accepted in Claude
// Code's own state file, where the key stays: every session shares it.
//
// The session's state comes from `claude agents --json`, which lists every
// live session on the host, engine's or not. Busy there also covers a session
// whose turn has ended while a background job runs, and Claude Code takes
// typed input then, so the state hook's file tells the two apart. A usage
// stop is the limit hook's marker (LimitFile).
package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/session"
)

// Name is the harness's name.
const Name = "claude"

// tmpReads lets a Claude executor read back what it or its tests wrote to
// /tmp, such as screenshots, without a prompt. macOS resolves /tmp to
// /private/tmp.
var tmpReads = []string{"Read(//tmp/**)", "Read(//private/tmp/**)"}

// Adapter is the Claude Code adapter.
type Adapter struct{ env harness.Env }

// New is the adapter, reading and running through env.
func New(env harness.Env) Adapter { return Adapter{env: env} }

// Name is "claude".
func (Adapter) Name() string { return Name }

// Session is the launch of one session; Claude keeps nothing per session.
func (a Adapter) Session(string) harness.Launcher { return launcher(a) }

// HookedState is false: Claude Code lists its sessions itself.
func (Adapter) HookedState() bool { return false }

// CheckKickoff is the check session's first message: it reads the skill from
// the plugin directory the launch adds with --add-dir.
func (a Adapter) CheckKickoff(marker string) string {
	skill := filepath.Join(PluginDir(a.env.Var, a.env.Home), "skills", "engine", "SKILL.md")
	return "This is engine's preflight check, not a task: do exactly these steps, in order, and nothing else. " +
		"1. Read " + skill + " with the Read tool. " +
		"2. Run `echo ok > " + marker + "` with Bash, then read " + marker + " with the Read tool. " +
		"3. Run `gh api user --jq .login` with Bash. " +
		"4. Run `engine version` with Bash. " +
		"5. Run `echo done >> " + marker + "` with Bash. " +
		"6. Run `cd \"$HOME\" && tmux kill-window -t \"$TMUX_PANE\"` with Bash. " +
		"If a step is denied, skip it and go on."
}

// launcher trusts the folder in Claude Code's state file. Its file tools ask
// before reading outside the worktree, which would stop an unattended
// executor, so it adds the engine plugin's directory with --add-dir and allows
// reads in /tmp with --allowedTools. /tmp only gets a read rule: Claude loads
// skills from every --add-dir directory, and anyone on the host can write to
// /tmp. The status line is engine's, which records the session's quota. The
// kickoff comes last, after --, because both options take every argument up
// to the next option.
type launcher Adapter

// Join is false: Claude's launch line names the session for Remote Control.
func (launcher) Join(string, string) (bool, string) { return false, "" }

func (l launcher) Trust(dir, _ string, _ bool) error {
	if err := TrustFolder(Config(l.env.Var, l.env.Home), dir); err != nil {
		return fmt.Errorf("trust %s for Claude Code: %w", dir, err)
	}
	return nil
}

// Release leaves the folder trusted: the trust is a key in a file Claude
// Code shares with every session.
func (launcher) Release(string) error { return nil }

func (l launcher) Env(bool) string {
	if dir := l.env.Var("CLAUDE_CONFIG_DIR"); dir != "" {
		return "export CLAUDE_CONFIG_DIR=" + hosts.ShellWord(dir) + " && "
	}
	return "unset CLAUDE_CONFIG_DIR && "
}

func (l launcher) Args(s harness.Launch) string {
	args := " --add-dir " + hosts.ShellWord(PluginDir(l.env.Var, l.env.Home)) + " --allowedTools"
	for _, rule := range tmpReads {
		args += " " + hosts.ShellWord(rule)
	}
	args += " --settings " + hosts.ShellWord(`{"statusLine":{"type":"command","command":"engine hook statusline"}}`)
	return args + " -- " + hosts.ShellWord(s.Kickoff)
}

// Observe reads the quota the status line recorded, whether Claude is signed
// in, and each run's state from Claude Code's session list. A list Claude
// cannot give leaves every state unknown.
func (a Adapter) Observe(_ context.Context, host hosts.Host, runs map[string]session.Run) harness.Snapshot {
	var snap harness.Snapshot
	if q, ok := session.ReadQuota(a.env.Home, a.env.Time())[Name]; ok {
		snap.Quota = &q
	}
	var agents []Agent
	if host.Runs(Name) {
		bin, err := host.BinPath(Name, a.env.Home)
		if err == nil && a.env.Exec != nil {
			snap.SignedOut = signedOut(a.env.Exec(bin, "auth", "status"))
			agents, err = ListAgents(a.env.Exec, bin)
		}
		if err != nil {
			snap.Error = err.Error()
			return snap
		}
	}
	// A host that does not run Claude lists no sessions, but a limit marker
	// still says a window's session is limited.
	snap.Sessions = map[string]harness.State{}
	for name, r := range runs {
		if st, ok := probe(name, r, agents); ok {
			snap.Sessions[name] = st
		}
	}
	return snap
}

// probe is the state of the window called name, whose newest run is r: the
// state of the session of that name, unless a limit marker makes a live,
// non-busy session limited.
func probe(name string, r session.Run, agents []Agent) (harness.State, bool) {
	var st harness.State
	for _, ag := range agents {
		if ag.Name == name {
			st.State = AgentState(ag)
			if st.State == session.StateWaiting {
				st.WaitingFor = ag.WaitingFor
			}
			st.Background = st.State == session.StateWorking && turnEnded(r.Dir, ag.SessionID)
			break
		}
	}
	if l, ok := ReadLimit(r.Dir); ok && st.State != session.StateWorking {
		st = harness.State{State: session.StateLimited, ResetsAt: l.ResetsAt}
	}
	return st, st.State != ""
}

// turnEnded reports whether a session that Claude Code lists as busy has
// ended its last turn: the newest hook event in its run's state file is a
// Stop, from the same session when both name one. Claude Code stays busy
// while a background job runs after the turn. A missing or unreadable file
// is not known to have ended, so the session counts as busy in a turn.
func turnEnded(dir, sessionID string) bool {
	st, ok := session.ReadHarnessState(dir)
	if !ok || st.Event != "Stop" || st.State != session.StateWaiting {
		return false
	}
	return st.SessionID == "" || sessionID == "" || st.SessionID == sessionID
}

// signedOut reads `claude auth status`, which prints its JSON and exits
// non-zero when signed out. Output it cannot read is not signed out.
func signedOut(out string, err error) bool {
	var exit *session.ExitError
	if err != nil && !errors.As(err, &exit) {
		return false
	}
	var status struct {
		LoggedIn *bool `json:"loggedIn"`
	}
	if json.Unmarshal([]byte(out), &status) != nil || status.LoggedIn == nil {
		return false
	}
	return !*status.LoggedIn
}

var _ harness.Adapter = Adapter{}
