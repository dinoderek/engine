# Codex sessions

> **Owns:** how engine runs Codex sessions: the launch line and tier models, the sandbox and its exceptions, the per-session settings, the session signals, and the remote-control app. **Not here:** host config and session lifecycle (`docs/hosts-and-sessions.md`), what the executor does (`docs/execution-protocol.md`).

## Launch

A host lists `codex` in its `harnesses` and pins its `bin` (`hosts-and-sessions.md`).
`engine start --harness codex`, or a card whose Harness is `codex`, launches
it, and `engine start --check --harness codex` checks it
(`hosts-and-sessions.md`, "Starting a session").
The harness's launch line and tiers:

```toml
[harness.codex]
max_sessions = 2
launch = "{bin} --sandbox workspace-write -a on-request -c approvals_reviewer=auto_review -c sandbox_workspace_write.network_access=true -c 'sandbox_workspace_write.writable_roots=[\"{clone}\"]' -m {model} -c model_reasoning_effort={effort}"
tiers.deep     = { model = "gpt-6.1-sol", effort = "high" }   # not Astra: Sol at high is close on coding, far cheaper
tiers.standard = { model = "gpt-6.1-sol", effort = "medium" }
tiers.light    = { model = "gpt-6-luna",  effort = "medium" }
```

`{model}` and `{effort}` come from the tier. `{clone}` is engine's clone of
the repo. It sits inside quotes, so it must expand to a plain word: engine's
clone paths are, under a home directory whose path is one, and a launch line
with `{clone}` fails otherwise.

## Starting

`spawn` writes engine's profile for the session,
`$CODEX_HOME/engine-<repo>-<task>.config.toml` (`~/.codex` without
`CODEX_HOME`), which trusts the worktree and its resolved path. After the
launch line come `-p engine-<repo>-<task>`, which layers that profile on the
user's config, `--dangerously-bypass-hook-trust`, then `--` and the kickoff.
`release` deletes the profile. The window also exports `ENGINE_HARNESS=codex`.

- **Folder trust.** The profile holds it: a session starts without the
  trust prompt, and the user's `config.toml` is untouched. A `-c
  'projects."<path>".trust_level=…'` does not hold it; the prompt still shows.
  A profile makes Codex run embedded, without the shared app-server; a joined
  session ignores it ("Apps").
- **Hook trust.** Codex runs a hook only once its hash is trusted, and
  otherwise asks for a review first, which would stop an unattended start.
  Engine-launched sessions bypass that, as Claude sessions run a repo's hooks
  once engine trusts the folder. The bypass is per invocation, and covers every
  enabled hook.

## Installing the skill

`plugins/engine/.codex-plugin/plugin.json` points at the `skills/` tree the
Claude plugin shares, and registers `engine hook state`, `engine hook
permission` and `engine hook pre-tool-use` on their events. The repo's
`.agents/plugins/marketplace.json` lists it. `codex plugin marketplace add
dinoderek/engine` then `codex plugin add engine@engine` install it. The two
manifests carry one version: the versions lane fails when they differ.
In a session engine did not launch, Codex runs a plugin's hooks only once
they are reviewed and trusted: review them in the first such session after an
install and after each update.

## Permission mode

Sessions default to auto mode: the `workspace-write` sandbox, with Codex's
auto-review deciding any action that crosses the sandbox boundary. With
`approval_policy = "never"` there would be nothing for it to review.

The sandbox needs two exceptions for executors:

- **Network access** (`sandbox_workspace_write.network_access`), for `gh`
  and `git push`.
- **Engine's clone as a writable root**
  (`sandbox_workspace_write.writable_roots`), because a worktree's git
  metadata lives in the main clone's `.git/worktrees/`, outside the worktree.

All Codex settings are passed per session, with `-c` or the session's
profile. The host's own `~/.codex/config.toml` is never edited.

## Guards

`engine hook pre-tool-use` and `engine hook permission` read Codex's events as
they read Claude's (`claude.md`, "Hooks"): Codex's shell
call is a `Bash` tool call with the same `tool_input.command`. The merge guard
behaves the same.

Codex sends `PermissionRequest` before auto-review, so a `deny` there would
pre-empt the review. With `ENGINE_UNATTENDED=1` and `ENGINE_HARNESS=codex`,
the permission hook only logs the request, as `left to auto-review`, and
auto-review, which never prompts, decides.

## Session signals

Codex's hooks run `engine hook state`, which writes `harness-state.json` to the
run directory, atomically: the state (`working`, `waiting` or `ended`), the
event that set it, the time and Codex's session ID. A prompt, or a finished
tool call, makes the session `working`: the call after an approved permission
request is how a `waiting` session resumes. A session start, `Stop`,
`Interrupt` or attended permission request makes it `waiting`, and a session
end `ended`. Outside an engine run the hook does nothing, and no hook ever
fails a Codex turn. `status` reads the file for a window whose newest run is
Codex's. A missing or unreadable file is unknown, never dead, and so is
`ended` while the window lives. Codex's `notify` is not used, because it holds
a single program and other tools already claim it (Computer Use on the Mac).

## Check

`engine start --check --harness codex` is the Claude check
(`hosts-and-sessions.md`) with Codex's kickoff, which reads files through the
shell. A Codex check fails when its window does not close (the report shows the
last screen, where a prompt would be), or a step is skipped, or the session's
hooks wrote no `harness-state.json`: that file is the proof they ran. Requests
left to auto-review are listed, and fail nothing.

## Setup

`engine setup` (`operator-setup.md`) gives a host that lists `codex` engine's
own Codex home, `~/.local/share/engine/codex`, so engine's sessions have config
apart from the operator's own, which setup never edits:

- **`config.toml`.** Setup owns a block at its top: auto-review, the
  `workspace-write` sandbox, `network_access`, and engine's clones as a writable
  root. Hook trust is left to the launch. Codex's own tables follow the block
  (the marketplace and plugin entries it writes, and folder trust). Setup
  replaces the block and keeps the rest, then has Codex read the file strictly.
- **The login** is a link to the operator's `auth.json`. A file already there is
  engine's own login and stays.
- **The plugin** installs from this repo's marketplace into that home, not the
  operator's.
- **The daemon** is `codex remote-control start` in that home, with the machine
  user's token in its environment, read from its file.

## Apps

The apps list sessions on a host's remote-control daemon (`codex remote-control
start`). A session engine launches runs embedded, because of its profile
("Starting"), so the apps do not show it. `spawn` can join a connected daemon:
with `Deps.JoinRemote` set, it reads the daemon's state with `codex
remote-control --json`, which changes nothing, and adds `--remote unix://` only
when the state is `connected`. In any other state (disabled, connecting,
errored, a failed or unreadable reply) the session starts embedded and
`spawn`'s reply warns, naming `codex remote-control start`; a down daemon never
refuses a spawn. Nothing sets `JoinRemote`, because a joined session
differs from an embedded one (Codex 0.164.0):

- The agent and its hooks run inside the daemon. They see the daemon's
  environment, not the window's: `GH_TOKEN` and `ENGINE_*` exported by the
  window are lost. `-c shell_environment_policy.set={…}` reaches the agent's
  commands, not the hooks, which get only the event, including its `cwd`.
- The session's profile (`-p`) is ignored. Folder trust comes from the
  daemon's `config.toml`, one entry per folder (a parent does not cover its
  children, and `-c projects."…".trust_level` does not stop the prompt).
- `-c sandbox_workspace_write.writable_roots` and `--add-dir` are refused with
  `--remote`: the daemon's `config.toml` holds them. `network_access`,
  auto-review and the sandbox mode work from `-c` or from that file.
- Hooks from the daemon's Codex home run in the daemon, and need
  `--dangerously-bypass-hook-trust` on the TUI, or a review prompt stops the
  session.

A daemon started with its own `CODEX_HOME` reads its own `config.toml` and
`hooks.json`, and a TUI run with the same `CODEX_HOME` joins it with `--remote
unix://`, so engine's sessions can have a config apart from the operator's
manual ones. Codex has no flag that names a session, so the apps show its thread
title, not `<repo>-<task>`, and the kickoff's `rc=` is unused. The preflight
check never joins.
