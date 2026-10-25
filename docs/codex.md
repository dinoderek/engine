# Codex sessions

> **Owns:** how engine runs Codex sessions: the launch line and tier models, the sandbox and its exceptions, the per-session settings, the session signals, and the remote-control app. **Not here:** host config and session lifecycle (`docs/hosts-and-sessions.md`), what the executor does (`docs/execution-protocol.md`).

## Launch

A host lists `codex` in its `harnesses` and pins its `bin` (`hosts-and-sessions.md`).
Engine does not start Codex sessions yet (`hosts-and-sessions.md`, "Starting a
session"). The harness's launch line and tiers:

```toml
[harness.codex]
max_sessions = 2
launch = "{bin} --sandbox workspace-write -a on-request -c approvals_reviewer=auto_review -c sandbox_workspace_write.network_access=true -c 'sandbox_workspace_write.writable_roots=[\"{clone}\"]' -m {model} -c model_reasoning_effort={effort}"
tiers.deep     = { model = "gpt-6.1-sol", effort = "high" }   # not Astra: Sol at high is close on coding, far cheaper
tiers.standard = { model = "gpt-6.1-sol", effort = "medium" }
tiers.light    = { model = "gpt-6-luna",  effort = "medium" }
```

`{model}` and `{effort}` come from the tier. `{clone}` is engine's clone of
the repo.

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

All Codex settings are passed per session with `-c`. The host's own
`~/.codex/config.toml` is never edited.

## Guards

`engine hook pre-tool-use` and `engine hook permission` read Codex's events as
they read Claude's (`claude.md`, "Hooks"): Codex's shell
call is a `Bash` tool call with the same `tool_input.command`, and its
`PermissionRequest` takes the same `deny` decision. The guards behave the same.

## Session signals

Codex's hooks run `engine hook state`, which writes `harness-state.json` to the
run directory, atomically:
the state (`working`, `waiting` or `ended`), the event that set it, the time
and Codex's session ID. A prompt, or a finished tool call, makes the session `working`: the call
after an approved permission request is how a `waiting` session resumes. A session start,
`Stop`, `Interrupt` or attended permission request makes it `waiting`, and a
session end `ended`. Outside an engine run the hook does nothing, and no hook
ever fails a Codex turn. `status` reads it for a window whose newest run is
Codex's. A missing or unreadable file is unknown, never dead, and so is
`ended` while the window lives. Codex's `notify` is not used, because it holds
a single program and other tools already claim it (Computer Use on the Mac).

## Apps

The host runs the remote-control app-server (`codex remote-control start`),
and the session's TUI connects to it with `--remote`.
