# Claude Code sessions

> **Owns:** how engine runs Claude Code sessions: the launch line and tier models, the flags and folder trust set at start, permission mode, the plugin's hooks, the session list, and Remote Control. **Not here:** host config and session lifecycle (`docs/hosts-and-sessions.md`), what the executor does (`docs/execution-protocol.md`).

## Launch

A host lists `claude` in its `harnesses` and pins its `bin` (`hosts-and-sessions.md`). The harness's launch line and tiers:

```toml
[harness.claude]
max_sessions = 8        # across all hosts: a budget lever
launch = "{bin} --permission-mode auto --model {model} --effort {effort} -n {name} --remote-control {name}"
tiers.deep     = { model = "claude-opus-5-5",   effort = "high" }
tiers.standard = { model = "claude-sonnet-5-5", effort = "high" }
tiers.light    = { model = "claude-haiku-5-5",  effort = "medium" }
```

`{name}` is the session name, `<repo>-<task>`, and `{model}` and `{effort}` come
from the tier.

## Starting

`engine start` (`hosts-and-sessions.md`, "Starting a session") does two things
only Claude needs:

- It sets `projects.<worktree>.hasTrustDialogAccepted` in `~/.claude.json` (or
  `$CLAUDE_CONFIG_DIR`'s), so the trust dialog, whose default exits, never
  shows.
- It adds `--add-dir <engine's plugin directory>` and a read-only rule for
  `/tmp`. An added directory's skills load, and anyone can write to `/tmp`, so
  reading the skill or a test's screenshots needs no prompt.

## Permission mode

Sessions run in **auto mode**, whose classifier approves or refuses most
actions without a prompt. The launch line sets `--permission-mode auto`. Some
actions still prompt. In a hands-off session the plugin's permission hook
denies every prompt instead of showing it, so the executor finds another route
or raises a hard stop.

## Hooks

The plugin ships a `PreToolUse` hook, `engine hook pre-tool-use`. It blocks a
Bash call that:

- runs `gh pr merge` without `--match-head-commit`, or with `--auto` or
  `--admin`;
- merges through `gh api`, REST or GraphQL.

`engine gh …` counts as `gh` for both.

It also ships `engine hook permission`, run on `PermissionRequest` and
`PermissionDenied`. When `ENGINE_UNATTENDED=1`, it denies every permission
request with a message naming the Permission hard stop, instead of a prompt
nobody would answer. It logs each request it denies, and in any session each
auto-mode refusal, to the run directory (`telemetry.md`). Without
`ENGINE_UNATTENDED`, Claude Code prompts as usual.

## Session list

`claude agents --json` on the host lists every live session, including those
in tmux, with its `pid`, `cwd`, `name`, `sessionId` and `status`. `busy` is
working; `waiting` and `idle` are waiting for input, because only a prompt
moves the session on. `status` gives a window whose newest run is Claude's the
state of the session of its name. `engine ps` finds one started by hand by its
name (`<repo>-<task>`), then, on its own host only, by its worktree (its
branch, in the claim's repo).

## Apps

The launch line's `-n {name} --remote-control {name}` sets the session's name,
`<repo>-<task>`, and shows it in the apps under that name. Remote Control needs
the host signed in with a claude.ai account (`operator-setup.md`).
