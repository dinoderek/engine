# Codex sessions

> **Owns:** how engine runs Codex sessions: the launch line and tier models, the sandbox and its exceptions, the per-session settings, the session signals, and the remote-control app. **Not here:** host config and session lifecycle (`docs/hosts-and-sessions.md`), what the executor does (`docs/execution-protocol.md`).

## Launch

A host lists `codex` in its `harnesses` and pins its `bin` (`hosts-and-sessions.md`).
A task whose harness is `codex` launches it, and `engine start --check
--harness codex` checks it (`hosts-and-sessions.md`, "Starting a session").
The harness's launch line and tiers:

```toml
[harness.codex]
max_sessions = 2
launch = "{bin} --sandbox workspace-write -a on-request -c approvals_reviewer=auto_review -c sandbox_workspace_write.network_access=true -m {model} -c model_reasoning_effort={effort}"
tiers.deep     = { model = "gpt-6.1-sol", effort = "high" }   # not Astra: Sol at high is close on coding, far cheaper
tiers.standard = { model = "gpt-6.1-sol", effort = "medium" }
tiers.light    = { model = "gpt-6-luna",  effort = "medium" }
```

`{model}` and `{effort}` come from the tier. The line sets no writable root
and no `--add-dir`: a joined session refuses both ("Apps"), so a line that
has either keeps every session embedded, and `spawn` warns.

## Starting

`spawn` joins engine's remote-control daemon when it can ("Apps"), and
otherwise starts the session embedded. Either way the window exports
`ENGINE_HARNESS=codex`, and `--dangerously-bypass-hook-trust`, then `--` and
the kickoff, follow the launch line.

- **Joined.** The window runs with `CODEX_HOME` at engine's Codex home
  ("Setup"). `spawn` adds the worktree and its resolved path to that home's
  `config.toml`, one `[projects."<path>"]` table each, and `release` removes
  them. After the launch line come `-C <worktree>`, without which the daemon
  starts the thread in its own working directory, `-c
  shell_environment_policy.set={…}`,
  which gives the agent's commands `ENGINE_RUN_DIR`, `ENGINE_HARNESS`, in
  hands-off mode `ENGINE_UNATTENDED`, and the window's `TMUX` and
  `TMUX_PANE`, then `--remote unix://`.
- **Embedded.** The window keeps the host's own `CODEX_HOME`. `spawn` writes
  engine's profile for the session,
  `$CODEX_HOME/engine-<repo>-<task>.config.toml` (`~/.codex` without
  `CODEX_HOME`), which trusts the worktree and its resolved path and makes
  engine's clone a writable root, and adds `-p engine-<repo>-<task>`, which
  layers it on the user's config. `release` deletes it.

- **Folder trust.** A `-c 'projects."<path>".trust_level=…'` does not hold
  it; the prompt still shows. The profile or engine's `config.toml` does, and
  the operator's own `config.toml` is untouched.
- **Hook trust.** Codex runs a hook only once its hash is trusted, and
  otherwise asks for a review first, which would stop an unattended start.
  Engine-launched sessions bypass that, as Claude sessions run a repo's hooks
  once engine trusts the folder. The bypass is per invocation, and covers every
  enabled hook.

## Installing the skills

`plugins/engine/.codex-plugin/plugin.json` points at the `skills/` tree the
Claude plugin shares, and registers `engine hook <entry> --harness codex`
for the `state`, `permission` and `pre-tool-use` entries on their events. The repo's
`.agents/plugins/marketplace.json` lists it. `codex plugin marketplace add
dinoderek/engine` then `codex plugin add engine@engine` install it. The two
manifests carry one version: the versions lane fails when they differ.
In a session engine did not launch, Codex runs a plugin's hooks only once
they are reviewed and trusted: review them in the first such session after an
install and after each update.

## Permission mode

Sessions default to auto mode: the `workspace-write` sandbox, with Codex's
auto-review deciding any action that crosses the sandbox boundary (never
`approval_policy = "never"`, which leaves it nothing to review).

The sandbox needs two exceptions for executors:

- **Network access** (`sandbox_workspace_write.network_access`), for `gh`
  and `git push`.
- **Engine's clone as a writable root**
  (`sandbox_workspace_write.writable_roots`), because a worktree's git
  metadata lives in the main clone's `.git/worktrees/`, outside the worktree.
  An embedded session's profile names the clone; engine's `config.toml`
  names every clone, for joined sessions.

The other settings are passed per session, with `-c`. The host's own
`~/.codex/config.toml` is never edited.

## Guards

The `pre-tool-use` and `permission` hooks read Codex's events as they read
Claude's (`claude.md`, "Hooks"): Codex's shell
call is a `Bash` tool call with the same `tool_input.command`. The merge guard
behaves the same.

Codex sends `PermissionRequest` before auto-review, so a `deny` there would
pre-empt the review. In an unattended Codex run
(`ENGINE_UNATTENDED=1`, or found as "Session signals" says), the permission hook
only logs the request, as `left to auto-review`, and auto-review, which never
prompts, decides.

## Session signals

Codex's hooks run `engine hook state --harness codex`, which writes `harness-state.json` to the
run directory, atomically: the state (`working`, `waiting` or `ended`), the
event that set it, the time and Codex's session and turn IDs. A prompt, or a finished
tool call, makes the session `working`: the call after an approved permission
request is how a `waiting` session resumes. A session start, `Stop`,
`Interrupt` or attended permission request makes it `waiting`, and a session
end `ended`. A session that joined a remote-control daemon runs its hooks in
the daemon's environment, which has no `ENGINE_RUN_DIR`, `ENGINE_HARNESS` or
`ENGINE_UNATTENDED`. Without `ENGINE_RUN_DIR`, the state and permission hooks
take the event's `cwd`, find the run whose `run.json` names the deepest
worktree holding it (the newest, if several), and read the mode from that
record. A Codex hook takes only a Codex run: a Claude session
always has its variables, and one opened by hand in a run's worktree is not
that run's.
Outside any run's worktree the hook does nothing, and no hook ever fails a
Codex turn. `status` reads the file for a window whose newest run is Codex's. A
missing or unreadable file is unknown, never dead, and so is `ended` while the
window lives. Codex's `notify` is not used, because it holds a single program
and other tools already claim it (Computer Use on the Mac).

`status` derives `limited` from waiting plus an exhausted quota window, or a
newer stored failed turn with `usageLimitExceeded`, even when the last hook
still says working. It reads only the newest turn with `thread/turns/list`,
without resuming or sending a prompt. Hooks retain `turn_id`; a different or
newer submitted turn wins over an old failure. Missing or failed history
reads keep the hook state. Queries share a three-second deadline and try
engine's Codex home, then the embedded home for missing threads.

`resets_at` is the latest reset among exhausted windows, or zero when unknown.
The coordinator retains an observed reset across quota expiry; a limit first
seen after expiry can have no known reset and is not resumed automatically.
An explicit hook `limited` supplies its own reset. Limited sessions never
stall. Stop/Interrupt hook inputs contain no limit error, so failed-turn
reads supplement ordinary hook waits. Hands-off launches suppress the
model-switch reminder with `notice.hide_rate_limit_model_nudge=true`, keeping
the selected model; gated launches keep the normal reminder.

## Quota

On a host that runs Codex, `status` starts a short-lived `codex app-server
--listen stdio://` with `CODEX_HOME` set to engine’s Codex home. After the
initialization handshake it calls `account/rateLimits/read`, then terminates
the process. Primary and secondary map to `five_hour` and `seven_day`;
`resetsAt` is absolute epoch seconds and `observed_at` is the call time.
Missing windows stay unknown. A failure or three-second deadline leaves
Codex quota unknown without failing `status`. Fleet consumers use the newest
reading per harness and clear usage after its reset.

## Check

`engine start --check --harness codex` is the Claude check
(`hosts-and-sessions.md`) with Codex's kickoff, which reads files through the
shell. A Codex check fails when its window does not close (the report shows the
last screen, where a prompt would be), or a step is skipped, or the session's
hooks wrote no `harness-state.json`: that file is the proof they ran. Requests
left to auto-review are listed, and fail nothing. The check joins engine's
daemon as a spawn would, and then writes a `run.json` naming its folder, which
the joined hooks find their run by; the report says whether it joined, and if
not, why.

## Setup

`engine setup` (`operator-setup.md`) gives a host that lists `codex` engine's
own Codex home, `~/.local/share/engine/codex`, for engine's sessions to join, apart from the
operator's own home, which setup never edits. A session that cannot join
still launches embedded, with the host's `CODEX_HOME` ("Starting", "Apps"):

- **`config.toml`.** Setup owns a block at its top: auto-review, the
  `workspace-write` sandbox, `network_access`, and engine's clones as a writable
  root. Hook trust is left to the launch. Codex's own tables follow the block
  (the marketplace and plugin entries it writes, and folder trust). Setup
  replaces the block and keeps the rest.
- **The login** is a link to the operator's `auth.json`. A file already there is
  engine's own login and stays.
- **The plugin** installs from this repo's marketplace into that home, not the
  operator's.
- **The daemon** is `codex remote-control start` in that home, without a session's
  `ENGINE_*`, `TMUX` and `TMUX_PANE`, which joined hooks would otherwise take
  for their own when setup runs inside a session. The start exits 1 when
  the connection to the apps is errored, though the daemon runs with remote
  control on, so setup judges it by the daemon afterwards ("Apps").
  Setup records each started daemon's PID and start time in
  `daemon-environment`. A running daemon without a matching receipt leaves
  setup open: finish its sessions, run the printed stop command, and rerun
  setup so the replacement inherits the host environment.

## Apps

The apps list sessions on a host's remote-control daemon (`codex remote-control
start`). With `Deps.JoinRemote` set, as the `engine` commands set it, `spawn`
joins engine's own daemon when it runs in engine's Codex home with remote
control on: `codex app-server daemon version` there, which starts nothing,
reports it running, its process carries `--remote-control`, and its
PID/start-time receipt matches. An unrecorded environment falls back to
embedded execution until setup refreshes the daemon. Otherwise
(no daemon, remote control off, a reply that cannot be read, or a launch line
with a writable root or `--add-dir`) the session starts embedded, and
`spawn`'s reply warns, saying why and that `engine setup` starts the daemon; a
down daemon never refuses a spawn.

`--remote-control` only asks for a connection to the apps. Whether the
daemon has one is read with `sqlite3` from its own log, `logs_2.sqlite` in
engine's Codex home: the newest remote-control websocket record from the
daemon's process (`process_uuid` is `pid:<pid>:<uuid>`) since its start
(`ps -o etime=`, which no locale or time zone changes, with 30 seconds'
slack). A connection record is connected; another record, or none, is not
connected, with that line or "no connection logged" as the reason. A log,
reply or start that cannot be read, such as with no `sqlite3`, is unknown. A
joined session still joins either way; `spawn` and `engine start --check`
warn that the apps do not show it, or may not. `engine setup` reports
`remote control on, connected`, `not connected (<reason>)` or `connection
unknown (<reason>)`, and stays done. `codex remote-control --json` is no read:
without a daemon it serves in the foreground, and it reports the machine's
connection, not the daemon's.

A joined session differs from an embedded one (Codex 0.164.0), which is why
"Starting" launches it differently:

- The agent and its hooks run inside the daemon. They see the daemon's
  environment, not the window's: `ENGINE_*` exported by the
  window are lost. `-c shell_environment_policy.set={…}` reaches the agent's
  commands, not the hooks, which get only the event, including its `cwd`
  ("Session signals").
- The session's profile (`-p`) is ignored. Folder trust comes from the
  daemon's `config.toml`, one entry per folder (a parent does not cover its
  children, and `-c projects."…".trust_level` does not stop the prompt).
- `-c sandbox_workspace_write.writable_roots` and `--add-dir` are refused with
  `--remote`: the daemon's `config.toml` holds them. `network_access`,
  auto-review and the sandbox mode work from `-c` or from that file.

Engine's daemon reads its own home's `config.toml` and `hooks.json`, apart
from the operator's. It is named after the host: Codex has no override.
Codex has no flag that names a session, so the kickoff's `rc=` is unused.
From a joined task session's first prompt, the state hook starts `engine hook
label --harness codex <run-dir> <thread>` in the background (hooks run inside the daemon), which calls
the daemon via `codex app-server proxy`: `thread/name/set` titles it
`<plan> > <task>`, or the task alone, and experimental project
calls file it under the repo's project, found by its `engine_repo` metadata.
Failures retry on later events, 15 seconds apart, three attempts max.
