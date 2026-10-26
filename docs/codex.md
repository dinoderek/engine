# Codex sessions

> **Owns:** how engine runs Codex sessions: the launch line and tier models, the sandbox and its exceptions, the per-session settings, the session signals, and the remote-control app. **Not here:** host config and session lifecycle (`docs/hosts-and-sessions.md`), what the executor does (`docs/execution-protocol.md`).

## Launch

A host lists `codex` in its `harnesses` and pins its `bin` (`hosts-and-sessions.md`).
The host's `spawn` and check ops launch Codex when the caller names it;
`engine start` does not yet (`hosts-and-sessions.md`, "Starting a session").
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
  A profile makes Codex run embedded, without the shared app-server, so a
  session joining the remote-control daemon needs another route: an entry in
  `~/.codex/config.toml`, removed at release, is the operator-approved one.
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

## Apps

The host runs the remote-control app-server (`codex remote-control start`),
and a TUI joins it with `--remote`. A session engine launches runs embedded,
because of its profile ("Starting"), so it does not join the app-server and
the apps do not show it.
