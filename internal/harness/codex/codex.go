// Package codex is the Codex adapter.
//
// A session runs joined to engine's own remote-control daemon when it can,
// so the ChatGPT and Codex apps list it, else embedded in its window. A
// joined session runs in engine's Codex home, whose config.toml trusts its
// folder; an embedded one runs in the host's own Codex home, with engine's
// profile for the session, which the launch selects with -p, trusting its
// folder, so the user's config.toml is never edited. Either way it runs the
// hooks of engine's Codex plugin without Codex's per-hash hook trust, whose
// review prompt would stop an unattended launch.
//
// Codex has no session list engine can ask, so a session's state comes from
// the state file its hooks write, supplemented by the daemon's stored turns
// (a usage-limit failure) and the account's rate limits. A joined session's
// hooks run inside the daemon, without the launch's variables, so they find
// their run by the event's cwd; the state hook then labels its thread.
package codex

import (
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/dinoderek/engine/internal/harness"
)

// Name is the harness's name.
const Name = "codex"

// Options replace the adapter's reads of Codex's app-server, for tests.
type Options struct {
	// Quota reads account/rateLimits/read in a Codex home. Nil starts a
	// short-lived app-server.
	Quota func(ctx context.Context, bin, home string) (json.RawMessage, error)
	// Turns reads the newest stored turn of each thread, without resuming
	// it. Nil starts a short-lived app-server.
	Turns func(ctx context.Context, bin, home string, ids []string) (map[string]json.RawMessage, error)
	// Label titles a thread and files it under a project, through the
	// daemon serving home. Nil uses `codex app-server proxy`.
	Label func(ctx context.Context, bin, home string, req LabelRequest) error
}

// Adapter is the Codex adapter.
type Adapter struct {
	env  harness.Env
	opts Options
}

// New is the adapter, reading and running through env.
func New(env harness.Env, opts Options) Adapter {
	if opts.Quota == nil {
		opts.Quota = readRateLimits
	}
	if opts.Turns == nil {
		opts.Turns = readTurns
	}
	if opts.Label == nil {
		opts.Label = labelThroughDaemon
	}
	return Adapter{env: env, opts: opts}
}

// Name is "codex".
func (Adapter) Name() string { return Name }

// Session is the launch of the session called name, whose embedded profile
// is named after it.
func (a Adapter) Session(name string) harness.Launcher {
	return launcher{Adapter: a, profile: Profile(name)}
}

// HookedState is true: only the hooks say what a Codex session does.
func (Adapter) HookedState() bool { return true }

// CheckKickoff is a Codex check session's first message: Codex reads files
// through its shell, and the skill comes from engine's Codex plugin.
func (Adapter) CheckKickoff(marker string) string {
	return "This is engine's preflight check, not a task: do exactly these steps, in order, and nothing else. " +
		"1. Read the SKILL.md of the engine plugin's engine skill. " +
		"2. Run `echo ok > " + marker + "`, then `cat " + marker + "`. " +
		"3. Run `gh api user --jq .login`. " +
		"4. Run `engine version`. " +
		"5. Run `echo done >> " + marker + "`. " +
		"6. Run `cd \"$HOME\" && tmux kill-window -t \"$TMUX_PANE\"`. " +
		"If a step is denied, skip it and go on."
}

// home is the host's own Codex home, which an embedded session runs in.
func (a Adapter) home() string { return Home(a.env.Var, a.env.Home) }

// HomeDir is engine's own Codex home, under home: the config, login link,
// plugin and app-server daemon that engine setup prepares for engine's Codex
// sessions to join, apart from the operator's own Codex home.
const HomeDir = ".local/share/engine/codex"

// EngineHome is engine's own Codex home, where engine setup runs engine's
// daemon.
func EngineHome(home string) string {
	return filepath.Join(home, filepath.FromSlash(HomeDir))
}

func (a Adapter) engineHome() string   { return EngineHome(a.env.Home) }
func (a Adapter) engineConfig() string { return filepath.Join(a.engineHome(), "config.toml") }

var _ harness.Adapter = Adapter{}
