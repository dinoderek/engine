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

`{name}` is `<repo>-<task>`; `{model}` and `{effort}` come from the tier.

## Starting

`engine start` (`hosts-and-sessions.md`, "Starting a session") adds Claude-specific setup:

- It sets `projects.<worktree>.hasTrustDialogAccepted` in `~/.claude.json` (or
  `$CLAUDE_CONFIG_DIR`'s), so the trust dialog, whose default exits, never
  shows.
- It adds `--settings` with a `statusLine` command, `engine hook statusline`,
  replacing the operator’s status line in engine sessions. The hook prints
  the session name and five-hour quota, recording changed subscription readings
  in the run directory (`telemetry.md`). Missing limits write nothing.
- It adds `--add-dir <engine's plugin directory>` and a read-only rule for
  `/tmp`. This permits reading skills and test screenshots without a prompt.

## Permission mode

Sessions run in **auto mode**, whose classifier approves or refuses most
actions without a prompt. Some actions still prompt. In a hands-off session the plugin's permission hook
denies every prompt instead of showing it, so the executor finds another route
or raises a hard stop.

## Hooks

`engine hook pre-tool-use` blocks Bash merges without
`--match-head-commit`, with `--auto` or `--admin`, or through the REST or
GraphQL API. `engine gh` counts as `gh`.

`engine hook permission` runs on `PermissionRequest` and `PermissionDenied`.
With `ENGINE_UNATTENDED=1` it denies prompts, naming the Permission hard stop;
otherwise Claude prompts. It records denials and auto-mode refusals
in the run directory (`telemetry.md`).

`engine hook limit` handles `StopFailure` errors `rate_limit|billing_error`,
writing `limit.json` and logging the full input. It takes `resets_at` from
the event or its `error_details` object, else the session's `quota.json`:
the latest reset among exhausted windows, else the five-hour reset.
`Notification` `quota_auto_resume_fired` and `UserPromptSubmit` clear the
marker; `quota_auto_resume_stale` and `quota_auto_resume_disabled` retain
it and record their type. Unknown resets stay zero. Claude's
`autoContinueAtUsageLimit` stays enabled.

## Session list

`claude agents --json` on the host lists every live session, including those
in tmux, with its `pid`, `cwd`, `name`, `sessionId` and `status`. `busy` is
working; `waiting` and `idle` are waiting for input, because only a prompt
moves the session on. For a window whose newest run is Claude's, `status`
takes the state of the session of the window's name. A limit marker makes
a live, non-busy window `limited`, with `resets_at`; busy wins. An unavailable
session list leaves state unknown. `engine ps` finds one
started by hand by its name (`<repo>-<task>`), then, on its own host only, by
its worktree (its branch, in the claim's repo).

## Apps

The launch flags name the session in the apps. Remote Control needs
the host signed in with a claude.ai account (`operator-setup.md`).
