# Harness adapters

Ephemeral design; never cite it. Status: **awaiting operator approval**.
Questions are marked **Q<n>**, each with a recommendation.

## Goal

Claude Code and Codex become two adapters behind one interface. The core
(`hostops`, `setup`, `hook`, `cli`, `reconcile`, `controlrun`) knows only the
interface and a harness's name as data. Every Codex app feature stays, inside
the Codex adapter: joining engine's daemon, thread titles, project filing and
the connection check. No engine session runs while this lands (it is built
outside engine), so nothing keeps backwards compatibility: hook command lines
and the status wire format change freely.

`reconcile` and `controlrun` are already harness-neutral: they carry harness
names as data, and read `Status.SignedOut`, `Window.State` and the quota
fields. They change only through their imports of `hostops` types.

## Packages

| Package | Holds | May import (first-party) |
| --- | --- | --- |
| `internal/harness` | the interfaces and their neutral types (`Launch`, `Snapshot`, `Event`, `Step`, `Outcome`, …), `Adapters` | `internal/hosts`, `internal/session` |
| `internal/harness/claude` | the Claude Code adapter | `internal/harness`, `internal/hosts`, `internal/session` |
| `internal/harness/codex` | the Codex adapter | `internal/harness`, `internal/hosts`, `internal/session` |
| `internal/harness/registry` | `New(harness.Env) harness.Adapters`: the one list of adapters, and the default | `internal/harness`, `internal/harness/claude`, `internal/harness/codex` |

**Deviation from the brief:** the registry is its own package, not part of
`internal/harness`. The adapters import `internal/harness` for its types, so
a registry inside it that imports them is an import cycle. The alternative,
adapters registering themselves in `init` with a blank import somewhere, hides
the list and still needs one package to import them; an explicit registry is
plainer.

**Composition roots.** The core gets adapters injected, never looked up:
`hostops.Deps.Harnesses` and `setup.Deps.Harnesses` are a `harness.Adapters`, and
`cli.Env.Harnesses` feeds the hook entries. Only the three packages that build
a `hostops.Deps` today, `internal/cli`, `internal/controlcmd` and
`internal/start`, import the registry. So hostops and reconcile tests inject a
fake adapter, and no core test needs a real harness.

## The interface

```go
package harness

// Env is the outside world an adapter reads and runs. Tests replace each one.
type Env struct {
	Exec   session.Runner      // the host's own CLIs
	Home   string
	Getenv func(string) string // nil reads as empty
	Now    func() time.Time
}

// Adapters is every adapter engine knows, by name. The registry builds it.
type Adapters interface {
	// Get returns the adapter called name; "" is the default (Claude today).
	Get(name string) (Adapter, bool)
	// Names lists them in a fixed order, for usage text, setup and release.
	Names() []string
}

// Adapter is everything engine does that differs between harnesses.
type Adapter interface {
	Name() string // as hosts.toml, run.json and ENGINE_HARNESS spell it

	// --- launch ---
	// Session returns the launcher for one session name: per-session
	// leftovers (Codex's profile) are named after it.
	Session(name string) Launcher

	// --- observe ---
	// Observe reads, once per status, what the harness says on this host:
	// its quota, its sign-in and the state of each run given (the newest run
	// of every live window that names this harness). It never fails status:
	// what it cannot read it leaves unknown, with the reason.
	Observe(ctx context.Context, host hosts.Host, runs []session.Run) Snapshot

	// --- hooks ---
	Hooks() Hooks

	// --- setup and check ---
	Setup() Setup
	// CheckKickoff is the preflight session's first message; marker is the
	// file it writes and reads back.
	CheckKickoff(marker string) string
	// HookedState is true when the session's state comes from its hooks, so
	// a check whose session wrote no state file fails (Codex).
	HookedState() bool
}

// Launcher is one session's launch. (Today's hostops.harness, minus Name.)
type Launcher interface {
	// Join decides, through bin, whether a session launched with line joins
	// the host's remote-control daemon. It only reads. warning says why it
	// does not, or why the apps may not show a joined session.
	Join(bin, line string) (joined bool, warning string)
	// Trust makes dir, a folder in clone, trusted, so no prompt blocks start.
	Trust(dir, clone string, joined bool) error
	// Release deletes the files Trust and the launch wrote for this session
	// alone: Codex's per-session profile, and dir's trust entry in engine's
	// Codex config.toml. Claude has none (its trust key sits in a file every
	// session shares). An empty dir removes only what needs no folder. It
	// runs on every adapter at release, whatever the run's harness: a spawn
	// that failed before run.json may have written them.
	Release(dir string) error
	// Env is the shell that exports the harness's config location, ending in
	// " && ".
	Env(joined bool) string
	// Args follow the launch line, ending in the kickoff.
	Args(l Launch) string
}

// Launch is what a window runs.
type Launch struct {
	Line, Dir, RunDir, Kickoff string
	Unattended, Joined         bool
	ServerURL, TokenFile, Attempt string // empty without the control server
}

// Snapshot is one Observe.
type Snapshot struct {
	Quota     *session.Quota      // nil: unknown
	SignedOut bool                // only when the harness's CLI says so
	Sessions  map[string]State    // by window name; absent is unknown
	Error     string              // why Sessions are unknown
}

// State is a live window's state, as hostops.Window reports it.
type State struct {
	State      string // session.StateWorking, StateWaiting, StateLimited
	WaitingFor string
	Background bool
	ResetsAt   int64
}
```

`Status` becomes harness-neutral at wire protocol 5 (Q1, decided): `quota[name]`
and `signed_out` as now, each window's state fields from `Sessions`, and a new
`harness_errors` (name → why its sessions are unknown) in place of the
Claude-shaped `agents` / `agents_error`, which nothing consumes. Codex still
reads quota before turns, because both happen inside one `Observe`.

### Hooks

```go
// Hooks translates a harness's hook events into engine's, and engine's
// replies back. The core owns the merge guard, the permission log and the
// state file; the adapter owns the event shapes and its own entries.
type Hooks interface {
	// Event reads one event in the harness's own JSON.
	Event(raw []byte) (Event, error)
	// Deny writes a permission request's denial in the harness's shape.
	Deny(w io.Writer, message string) error
	// Unattended is what an unattended session's permission request gets:
	// Deny (Claude), or LeaveToReview (Codex: auto-review decides after it).
	Unattended() PermissionPolicy
	// FindsRun is true when the hooks may run without ENGINE_RUN_DIR and
	// find their run by the event's cwd (a joined Codex session).
	FindsRun() bool
	// Signalled runs after the state hook recorded ev: Codex starts the
	// thread label here. It never fails the hook.
	Signalled(ctx HookContext, ev Event)
	// Entries are the adapter's own `engine hook <entry>` commands, such as
	// Claude's statusline and limit, and Codex's label.
	Entries() map[string]Entry
}

// Event is a hook event in engine's terms.
type Event struct {
	Kind      EventKind // PromptSubmitted, ToolStarting, ToolFinished, SessionStarted,
	                    // TurnStopped, Interrupted, PermissionRequested, PermissionDenied,
	                    // SessionEnded, Other
	Name      string    // the harness's own name, logged as-is
	SessionID, TurnID, Cwd string
	Tool      string
	Command   string    // the shell command, when Tool is the harness's shell
	Input     json.RawMessage
	Mode, Reason string
}
```

`Event.Name` keeps `permissions.jsonl` and `harness-state.json` byte-for-byte
as they are: their `event` field still holds `Stop`, `PostToolUse`, and so on.
`internal/hook` maps `Kind` to working / waiting / ended (today's `stateFor`)
and the merge guard reads `Command`.

### Setup

```go
// Setup is what an adapter adds to `engine setup` on one host.
type Setup interface {
	// Tools are the binaries the tools step requires ("claude"); a harness
	// whose binary hosts.toml pins checks it in its own step (Codex).
	Tools() []string
	// Steps run after the core's host steps, in order.
	Steps(t Target) []Step
	// CheckFailed is the open item when this harness's preflight fails.
	CheckFailed(t Target) (detail, command string)
}

// Target is the host being set up, as setup's runner reaches it.
type Target interface {
	Sh(command, stdin string) (Result, error) // runs on the host
	OnHost(command string, tty bool) string   // the command for the operator
	Remote() bool
	Rerun() string
	Missing(tool string) bool                 // the tools step did not find it
	Plugin() string                           // the caller's plugin version
	Host() hosts.Host
	Home() string
}

type Step struct {
	Name string
	Run  func() (Outcome, error)
}
```

`outcome`, `result` and the ok / changed / OPEN states move from `setup` to
`harness` as `Outcome` and `Result`, so adapters build them.

## Inventory: every site and where it goes

| Site today | Moves to |
| --- | --- |
| `hostops/harness.go`: `Claude`, `Codex`, `harness`, `harnessFor`, `claude{}`, `tmpReads` | interface → `harness`; `claude{}` → `harness/claude/launch.go`; `harnessFor` → `Adapters.Get`. `launch`/`launchCommand` stay in hostops (neutral), building `harness.Launch` |
| `hostops/codexharness.go`, `daemonenv.go` | `harness/codex/launch.go`, `join.go` |
| `hostops/status.go`: `probe` switch, `listAgents`, `turnEnded`, Claude limit read | switch → loop over snapshots; Claude parts → `harness/claude/observe.go` |
| `hostops/codexstate.go`, `codexturns.go`, `codexquota.go`, `codexws.go` | `harness/codex/observe.go`, `turns.go`, `quota.go`, `appserver.go`, `ws.go` |
| `hostops/codexlabel.go`, `cli/codexlabel.go` (`labelEvents`, detach) | `harness/codex/label.go`; `Signalled` decides; the detach is `HookContext.Detach` |
| `hostops/signin.go` | each adapter's `Observe` (`SignedOut`) |
| `hostops/release.go`: Codex untrust | loop: every adapter's `Session(name).Release(worktree)` |
| `hostops/check.go`: `h.Name() == Codex` (twice), `CodexCheckKickoff`, Claude kickoff path | `HookedState()`, `CheckKickoff()` |
| `hostops/hostops.go` `Deps.CodexQuota/CodexTurns/CodexLabel`, `JoinRemote` | Codex adapter's own options (tests inject); `JoinRemote` stays in hostops (it gates the call to `Join`) |
| `hostops/command.go` usage `claude\|codex` | built from `Adapters.Names()` |
| `hostops/permissions.go` comment on `hook.AutoReview` | the policy's logged decision string moves to `harness` (`LeaveToReview`) |
| `cli/cli.go`: `Codex: run.harness == hostops.Codex`, hook usage lines, `statusline`/`limit`/`codex-label` cases | neutral entries (below); usage from the set |
| `cli/hookrun.go`: `rec.Harness == hostops.Codex` | `adapter.Hooks().FindsRun()` |
| `hook/permission.go` `Codex bool` | `Policy harness.PermissionPolicy`, plus `Deny` from the adapter |
| `hook/state.go` event names | `Kind` from the adapter; the map stays in `hook` |
| `hook/limit.go`, `hook/statusline.go` | `harness/claude/limit.go`, `statusline.go` (Claude entries) |
| `hook/hook.go` (merge guard), `shell.go`, `ismerge.go` | stay: core. Reads `Event.Command` |
| `session/trust.go` (`ClaudeConfig`, `TrustFolder`, `PluginDir`) | `harness/claude/trust.go`. The atomic file helpers it shares with Codex trust (`replaceFile`, `stamp`) stay in `session` as exported helpers |
| `session/agents.go` (`ListAgents`, `Agent`), `state.go` `AgentState` | `harness/claude/agents.go`; `session.Agent` → `harness.LiveSession` |
| `session/limit.go` (`limit.json`) | `harness/claude`; `StateLimited` stays in `session/state.go` |
| `session/quota.go` | `Quota`, `QuotaWindow`, `MergeQuota` stay (neutral, used by reconcile); `ReadQuota` (scans `quota.json`, written by Claude's statusline) → `harness/claude` |
| `session/codex.go`, `codextrust.go` | `harness/codex/profile.go`, `trust.go`; `TOMLString` stays in session if anything neutral uses it, else moves |
| `hosts/codexremote.go`, `hosts.CodexHomeDir`, `ParseElapsed` | `harness/codex/connection.go`; `ParseElapsed` too (only Codex uses it) |
| `setup/codex.go`, `codexconfig.go`, `daemonenv.go` | `harness/codex/setup*.go` |
| `setup/steps.go`: `claude` in `tools`, `installPlugin`, `pluginInstalled`, `signIn`, the Claude fallback in `check` | `harness/claude/setup.go` |
| `setup/setup.go`: `codexHarness`, `r.codex`, fixed step numbers, `preflight`'s harness list | adapters' `Steps`; numbering sequential (**Q3**); preflight checks every harness the host runs, default first |
| `start/start.go`, `remote/remote.go` comments ("Claude when empty") | "the default harness when empty" |
| `hosts/launch.go` comment ("Codex's line quotes") | reworded |

## The hook CLI

New shape: `engine hook <entry> --harness <name> [args…]`. Every entry names
its harness on its own command line; the CLI resolves it through the registry
and names no harness itself.

- **Core entries** (in `cli`/`hook`, the same for every harness):
  `pre-tool-use` (the merge guard), `permission` (log, and the adapter's
  unattended policy), `state` (the state file, then `Signalled`).
- **Adapter entries**, looked up in the named adapter's `Entries()`:
  Claude's `statusline` and `limit`, Codex's `label <run-dir> <thread>`.
- **`--harness` is required.** Without it, or with a name the registry lacks,
  or an entry the adapter lacks, the hook fails open (exit 1, which blocks
  nothing). The run directory and unattended mode still come from the
  environment, or for a joined Codex session from the run found by cwd.
- **Hooks no longer read `ENGINE_HARNESS`**; the window still exports it.

| Today | After |
| --- | --- |
| `engine hook pre-tool-use` | `engine hook pre-tool-use --harness claude\|codex` |
| `engine hook permission` | `engine hook permission --harness claude\|codex` |
| `engine hook state --harness claude\|codex` | same |
| `engine hook statusline` (Claude's `--settings`) | `engine hook statusline --harness claude` |
| `engine hook limit` | `engine hook limit --harness claude` |
| `engine hook codex-label <dir> <thread>` | `engine hook label --harness codex <dir> <thread>` |

**Migration: none.** No engine session runs while this lands, so no old
command line keeps working and there is no alias. The manifests switch to the
new lines and the plugin version bumps in both `plugin.json` files; `engine
setup` installs it for Claude on each host and into engine's Codex home. The
operator's own Codex home updates by hand (`codex plugin update`).

**Q2 (hard stop: behaviour change).** The manifest is the plugin's hook
config (`plugins/engine/hooks/hooks.json`, `plugins/engine/.codex-plugin/plugin.json`):
the harness runs its command line and nothing else, so a hook learns its
harness only from its command line or its environment. A Claude reviewer
started inside a Codex run inherits `ENGINE_HARNESS=codex` and
`ENGINE_UNATTENDED=1`; today its permission hook leaves requests to an
auto-review Claude has not got, so Claude prompts in an unattended window.
With `--harness` required, the hook knows its real harness and denies, as for
every unattended Claude request. *Recommendation:* accept that fix.

## Import rules and the leak check

New rows in `scripts/import-rules.tsv`:

```
internal/harness	only	internal/hosts,internal/session
internal/harness/claude	only	internal/harness,internal/hosts,internal/session
internal/harness/codex	only	internal/harness,internal/hosts,internal/session
internal/harness/registry	only	internal/harness,internal/harness/claude,internal/harness/codex
```

Changed rows: `internal/hook` `only internal/harness`; `internal/hostops` and
`internal/setup` add `internal/harness`; `internal/cli`, `internal/controlcmd`
and `internal/start` add `internal/harness` and `internal/harness/registry`.
Every package already has an `only` row, so no row lists an adapter except the
registry's, and the `imports` lane rejects any other importer: no `forbid` row
is needed. `reconcile` and `controlrun` are unchanged.

**Leak check: a new required lane, `harness-names`** (`dev check
harness-names`, in `devtools/internal/checks/harnessnames.go`). It parses every
non-test Go file under `cmd/` and `internal/`, outside `internal/harness/`
(adapters, registry, interface), and fails on:

- a string literal containing `claude` or `codex`, any case, as a word part
  (catches `== "codex"`, `"claude agents"`, `"CODEX_HOME"`);
- an identifier containing `Claude` or `Codex`, any case (catches
  `hostops.Codex`, `codexTurns`).

Comments are not checked: prose like "a Claude reviewer a Codex executor
starts" may still explain a neutral rule. The reviewer keeps comments honest.
The names come from the lane's flags (`-names claude,codex -allow
internal/harness/`), so the lane row in `scripts/lanes.tsv` is the one place
that lists them. No baseline: the lane lands when the count is zero (last PR
of the split). Tests: a fixture tree with `if h == "codex"` and one with an
identifier `claudeBin` each fail with the file, line and name; a clean tree,
and the same code under the allowed prefix, pass. `quality-gates.md` gets its
lane row.

**Q4.** Is this lane a ruling I may add (it tightens, it loosens nothing),
or do you want to agree it? *Recommendation:* add it; log it as a ruling.

## Docs plan

New `docs/harnesses.md` (budget row **700**, under the 2000 default), owning:
the adapter contract (what an adapter provides and reports, what the core
does with it), the harness config an operator writes (`[harness.<name>]`
launch lines and tiers: they live only in the operator's `hosts.toml`, so
they are practice, not code), and pointers for operator practice. It replaces
`claude.md` and `codex.md`; their AGENTS.md row becomes "changes a harness
adapter or what the core asks of one → `docs/harnesses.md`, and the adapter's
package doc".

Legend: **K** keep (in harnesses.md, reworded and short), **G** move to a Go
doc comment in the adapter package, **O** move to `operator-setup.md`,
**C** move to `coordinator.md`, **D** delete (the code says it).

### `claude.md`

| Paragraph | Fate |
| --- | --- |
| Launch: the `[harness.claude]` block, `{name}` | K (config example) |
| Starting: `hasTrustDialogAccepted`, "default exits" | G (`claude` package doc: why trust is written) / D (key and path) |
| Starting: `--settings` statusLine, quota recording | G (why the statusline replaces the operator's) / D |
| Starting: `--add-dir`, `/tmp` read rule | D (`Args` already says why) |
| Permission mode: auto mode; hands-off denies prompts | K: one line of the contract table ("unattended permission requests: deny") |
| Hooks: state only in a Claude run | G (state hook doc) |
| Hooks: merge guard rules | K: one line ("`pre-tool-use` blocks unpinned, auto, admin and API merges; every merge in an engine session"); detail D (`hook` package doc owns it) |
| Hooks: permission entry | K (contract line) / D |
| Hooks: limit, `resets_at` fallback chain, notifications, `autoContinueAtUsageLimit` | G (`claude/limit.go`) |
| Session list: `claude agents --json`, busy/waiting/idle, limit marker | G (`claude/observe.go`) |
| Session list: background vs busy, hook disambiguation | G; K one contract line ("`background`: working with the turn ended; typed input is taken") |
| Apps: launch flags name the session; Remote Control needs claude.ai sign-in | O (already there) / D |

### `codex.md`

| Paragraph | Fate |
| --- | --- |
| Launch: `[harness.codex]` block, tier comment | K (config example) |
| Launch: line sets no writable root or `--add-dir` | K: one operator rule ("a line with either keeps every session embedded") |
| Starting: joined (`CODEX_HOME`, `[projects]`, `-C`, `shell_environment_policy`, `--remote`) | G (`codex` package doc: the joined/embedded split and why) / D (flags) |
| Starting: embedded profile path and `-p` | G / D |
| Folder trust: `-c` trust does not hold | G (the quirk) |
| Hook trust: per-hash review, bypass | G |
| Installing the skills | O (install, and review hooks in sessions engine did not launch, after each install and update); version lane sentence D (`versions.md` owns it) |
| Permission mode: sandbox + auto-review, never `approval_policy = "never"` | K: one operator rule for the launch line |
| Sandbox exceptions (network, clone as writable root) | G (why setup's block and the profile carry them) |
| Per-session `-c`; `~/.codex/config.toml` never edited | O ("setup never edits your Codex home") / D |
| Guards: shell call is a Bash call | G (`codex/hooks.go`) |
| Guards: PermissionRequest before auto-review → leave to review | K (contract line) + G (why) |
| Session signals: state file, events → states | G; K one line ("state from hooks; a missing file is unknown, never dead") |
| Session signals: joined hooks lose env, find run by cwd, only Codex runs | G |
| Session signals: `notify` unused | G |
| Limited: derivation, `thread/turns/list`, deadlines, homes order | G (`codex/observe.go`) |
| `resets_at`; coordinator keeps an observed reset; unknown reset not resumed | C (it is the coordinator's rule, for every harness; check `coordinator.md` already says it, else one sentence) |
| Hands-off hides the model nudge | G |
| Quota: app-server handshake, mapping, deadline | G (`codex/quota.go`); K one line ("fleet uses the newest reading per harness, cleared after its reset") |
| Check: Codex kickoff, fails without a state file, joins as a spawn | K (contract: `HookedState`) / G |
| Setup: engine's own Codex home, operator's untouched | O (already there) |
| Setup: `config.toml` block, login link, plugin, daemon start, exit 1 quirk | G (`codex/setup.go`) |
| Setup: receipt; a daemon without one leaves setup open; what to do | O (practice: finish its sessions, run the printed stop, rerun) / G (why) |
| Apps: when `spawn` joins; fallback to embedded; warnings | G / D |
| Apps: the sqlite log read, `ps etime`, 30 s slack, report strings | D (code) / G (why the log, not `--remote-control`, and why not `codex remote-control --json`) |
| Apps: joined-vs-embedded quirks (env lost, `-p` ignored, refused flags) | G (the canonical quirk list, Codex version noted) |
| Apps: daemon named after host; no session-name flag | G |
| Apps: labels, `engine_repo`, 15 s × 3 | G / D |

### Other docs

- **`coordinator.md`:** "`claude agents --json`" → "each adapter's
  observation"; the `status` row drops `claude agents`; the `release` row
  says "removes each adapter's per-session leftovers"; the session-signals
  list cites `harnesses.md` instead of the two docs; the background-job note
  keeps its operator meaning in one clause.
- **`hosts-and-sessions.md`:** "Launch lines and tier models are in…" →
  `harnesses.md`; start steps 3, 5 and 6 drop the Codex detail ("the adapter
  adds its flags; a joined session gets the variables on its command line"
  becomes "the adapter's launch" and one clause); preflight drops "(Claude
  without `--harness`)" for "the default harness"; the Apps row keeps the two
  app names and loads `harnesses.md`.
- **`telemetry.md`:** keeps one line per run-directory file, naming the
  adapter that owns `quota.json`, `limit.json` and `codex-label.json`; their
  fields move to doc comments beside the code that writes them.
- **`operator-setup.md`:** keeps the install, sign-in and plugin-update
  practice; gains the hook-review and daemon-receipt practice; drops the
  `codex.md` citations.
- **`execution-protocol.md`:** the merge-guard citation → `harnesses.md`.
- **`architecture.md`, `project-structure.md`:** the `internal/harness`
  packages, the composition roots, and the leak lane in one sentence each;
  `internal/session`'s and `internal/hosts`'s descriptions lose Claude trust,
  Codex profiles and Codex logs. Both are in the 4000-word always-load set;
  the edit must net to zero or less.
- **`quality-gates.md`:** one lane-table row for `harness-names`.
- **AGENTS.md:** the Claude/Codex row becomes the `harnesses.md` row.
- **`scripts/doc-budgets.tsv`:** add `docs/harnesses.md 700`. No budget rises.

Net: about 2,670 words of `claude.md` + `codex.md` become about 700, plus
doc comments.

## Test plan

- **Contract suite** (`internal/harness/harnesstest`, a test-only helper
  package imported only by tests): `Run(t, func(harness.Env) harness.Adapter,
  Fixtures)` checks for any adapter that `Name` is stable; `Session(n).Args`
  ends in `-- <kickoff>`; `Env` ends in ` && `; `Trust` then `Release` leaves
  no per-session file under a temp home; `Release("")` is harmless;
  `Observe` with a failing `Exec` returns unknowns, never a state, and never
  panics; `Hooks().Event` maps each fixture event to its `Kind` and keeps
  `Name`; `Deny` writes JSON or `Unattended` is `LeaveToReview`; every
  `Entries()` name is unique across the set; `Setup().Steps` names are
  non-empty. Each adapter supplies fixtures (its event JSON, CLI replies).
  Both adapters' `_test.go` call it.
- **Fake adapter** (`harnesstest.Fake`): records calls, returns canned
  snapshots and launch strings. `hostops` tests (spawn, status, check, release)
  and `reconcile` tests (via `hostops.Status` built from a fake's snapshot) use
  it; the Claude- and Codex-specific assertions move with their code.
- **Moved tests** keep their assertions: `codex*_test.go` in hostops, setup,
  session and hosts move to `harness/codex`; Claude's statusline, limit,
  trust, agents and sign-in tests to `harness/claude`. The process-level
  tests (`codexturns_process_test.go`, `setup/codexproc_test.go`) move as is.
- **CLI tests** prove every new command line runs its handler, and that a
  missing or unknown `--harness`, or an entry the named adapter lacks, fails
  open (exit 1).
- **Lane test** as in the leak check section.
- **Coverage** stays ≥ 80%; the lane names any file below, per PR.

## PR split

About 8,000 lines move (code and tests), so four PRs, each green and
behaviour-preserving on its own:

1. **Interface, registry, launch, observe, release.** `internal/harness*`,
   the fake and contract suite, hostops on the interface, the `session` and
   `hosts` moves, import rules. Docs: paths only.
2. **Hooks.** Neutral entries, adapter `Hooks`, manifests with `--harness`,
   plugin version bump, CLI tests (and the Q2 fix).
3. **Setup and check.** Adapter `Setup` steps and `CheckKickoff`.
4. **Leak lane and docs.** `harness-names` lane (now at zero), `harnesses.md`,
   doc comments, removal of `claude.md` and `codex.md`, the other docs' cuts.

**Q5.** Four PRs, or one? *Recommendation:* four, gated each; docs last
because `harnesses.md` describes the interface the first three build. The
operator's docs priority could instead go first as a docs-only PR that moves
the quirks into today's files' doc comments (they then move with the code).

## Decisions

- **Q1** No backwards compatibility: status wire protocol 5 (above).
- **Q2** Open: confirm the permission-harness fix (above).
- **Q3, Q3b, Q4, Q5, Q6** As recommended: sequential setup step numbers;
  a host runs only the setup steps of the harnesses it lists; the
  `harness-names` lane is a ruling; four PRs, docs last; fetch `origin/main`
  first and ask if the concurrent PRs are still open.
