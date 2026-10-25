# Codex sessions

> **Owns:** how engine runs Codex sessions: the launch line and tier models, the sandbox and its exceptions, the per-session settings, the session signals, and the remote-control app. **Not here:** host config and session lifecycle (`docs/hosts-and-sessions.md`), what the executor does (`docs/execution-protocol.md`).

## Launch

A host lists `codex` in its `harnesses` and pins its `bin` (`hosts-and-sessions.md`). The harness's launch line and tiers:

```toml
[harness.codex]
max_sessions = 2
launch = "{bin} --sandbox workspace-write -a on-request -c approvals_reviewer=auto_review -c sandbox_workspace_write.network_access=true -c 'sandbox_workspace_write.writable_roots=[\"{clone}\"]' -m {model} -c model_reasoning_effort={effort}"
tiers.deep     = { model = "gpt-6.1-sol", effort = "high" }   # not Astra: Sol at high is close on coding, far cheaper
tiers.standard = { model = "gpt-6.1-sol", effort = "medium" }
tiers.light    = { model = "gpt-6-luna",  effort = "medium" }
```

`{name}` is the session name, `<repo>-<task>`. `{model}` and `{effort}` come
from the tier. `{clone}` is engine's clone of the repo.

## Permission mode

Sessions run under the `workspace-write` sandbox, with Codex's auto-review
deciding any action that crosses the sandbox boundary. With
`approval_policy = "never"` there would be nothing for it to review.

The sandbox needs two exceptions for executors:

- **Network access** (`sandbox_workspace_write.network_access`), for `gh`
  and `git push`.
- **Engine's clone as a writable root**
  (`sandbox_workspace_write.writable_roots`), because a worktree's git
  metadata lives in the main clone's `.git/worktrees/`, outside the worktree.

All Codex settings are passed per session with `-c`. The host's own
`~/.codex/config.toml` is never edited.

## Hooks

Codex sessions have no guard yet. The merge and permission guards in
`claude.md` do not apply to them.

## Session signals

Codex's hooks write to the run directory. Codex's `notify` is not used,
because it holds a single program and other tools already claim it (Computer
Use on the Mac).

## Apps

The host runs the remote-control app-server (`codex remote-control start`),
and the session's TUI connects to it with `--remote`. Clients reach the
session through the ChatGPT or Codex apps.
