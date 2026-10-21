# Hosts and sessions

> **Owns:** host config, task placement, how sessions are started and reached. **Not here:** what a session does (the `engine` skill), what it records (`telemetry.md`).

## Hosts

A host is somewhere sessions run. Each one declares the harnesses it runs, how
many sessions it holds at once, and its capabilities. Capabilities are
declared, never inferred.

```toml
default_harness = "claude"

[harness.claude]
max_sessions = 8        # across all hosts: a budget lever
launch = "{bin} --permission-mode auto --model {model} --effort {effort}"
tiers.deep     = { model = "claude-opus-5-5",   effort = "high" }
tiers.standard = { model = "claude-sonnet-5-5", effort = "high" }
tiers.light    = { model = "claude-haiku-5-5",  effort = "medium" }

[harness.codex]
max_sessions = 2
launch = "{bin} --sandbox workspace-write -a on-request -c approvals_reviewer=auto_review -c sandbox_workspace_write.network_access=true -c 'sandbox_workspace_write.writable_roots=[\"{clone}\"]' -m {model} -c model_reasoning_effort={effort}"
tiers.deep     = { model = "gpt-6.1-sol", effort = "high" }
tiers.standard = { model = "gpt-6.1-sol", effort = "medium" }
tiers.light    = { model = "gpt-6-luna",  effort = "medium" }

[host.mac]
kind = "local"
harnesses = ["claude", "codex"]
max_sessions = 3
capabilities = ["ios", "android", "docker"]
bin = { claude = "~/.local/bin/claude", codex = "~/.local/bin/codex" }

[host.linux]
kind = "local"
harnesses = ["claude", "codex"]
max_sessions = 6
capabilities = ["docker"]
bin = { claude = "~/.local/bin/claude", codex = "~/.local/bin/codex" }
```

Each harness maps every tier (`execution-protocol.md`, "Models") to a model
and an effort level. GPT-6 Astra is deliberately unmapped: GPT-6.1 Sol at high
effort comes close to it on coding at a fraction of the cost.

Each host pins the path of every harness binary (`bin`), and `{bin}` in the
launch line expands to it. Engine never runs whichever install comes first on
`PATH`: this Mac has a second Codex under Homebrew that shadows the standalone
one.

Hosts are local or SSH machines only. Engine does not spawn cloud sessions.

## Placement

The coordinator places a ready task on a host that meets all of these:

- the host has a free slot;
- the harness (the card's, or the default) is under its own cap;
- the host's capabilities include everything in the card's `requires`.

Ties go to the host with the most free slots. A task that no host can take
stays ready, and the coordinator reports why. How the coordinator reaches each
host is in `coordinator.md`.

## Permission mode

Sessions default to **auto mode**, so they never stop for a permission prompt.
Only hard stops pause them.

- **Claude Code:** `--permission-mode auto`.
- **Codex:** the `workspace-write` sandbox, with Codex's auto-review deciding
  any action that crosses the sandbox boundary. With
  `approval_policy = "never"` there would be nothing for it to review.

The Codex sandbox needs two exceptions for executors:

- **Network access** (`sandbox_workspace_write.network_access`), for `gh`
  and `git push`.
- **Engine's clone as a writable root**
  (`sandbox_workspace_write.writable_roots`), because a worktree's git
  metadata lives in the main clone's `.git/worktrees/`, outside the worktree.

All Codex settings are passed per session with `-c`. The host's own
`~/.codex/config.toml` is never edited.

## Naming

Every surface uses one name per session, `<repo>#<task>`, for example
`boga3#412`. It is the tmux window, the remote-control session name and the
branch prefix. So a session is findable from whichever surface you are on.

## Reaching a session

| Session | Phone | Desktop | SSH / tmux |
| --- | --- | --- | --- |
| Claude CLI, local host | Claude app, via Remote Control | claude.ai/code, Claude desktop | yes |
| Codex CLI, local host | ChatGPT app, via Codex Remote (CLI pairing is experimental) | ChatGPT / Codex desktop | yes |

**Rule: tmux is the floor.** Every local session runs as a window in the tmux
session `engine` on its host, using the harness's interactive CLI (not
headless mode), so it can take questions. Where the harness supports remote
control, the session is also registered for it at launch. SSH plus
`tmux attach` therefore always works, and the apps are a bonus.

The coordinator's "started" comment on the task's issue says where the session
runs and how to attach: the host, the tmux target, and the remote-control name
or URL. `engine ps` lists every live session with the same details.

## Lifecycle

- **Start:** create the worktree, open a tmux window in it, launch the harness
  with the tier's model and effort and the kickoff prompt, and register
  remote control. The kickoff is `Execute task <tracker ref> with the engine
  executor protocol. Reviewer model: <deep model>`, the reviewer model taken
  from the same harness's `deep` tier.
- **tmux mechanics:**
  - Send text with `send-keys -l`, then send `Enter` as a separate call after
    a short delay. Input boxes can swallow a newline that arrives with the
    text.
  - Run `pipe-pane` into the run directory, so the raw terminal log outlives
    the tmux server.
  - Set `allow-rename off`, so agents can't rename windows.
- **Questions:** the executor sets `needs-human` and waits in the live session.
  Answer from any surface. A comment on the task's issue also works, because
  the executor reads its inbox when it resumes.
- **End:** after cleanup the executor exits. The coordinator closes the tmux
  window, and the slot frees.
- **Crash:** the window has gone but the task is still `running`. The
  coordinator marks it `failed` and keeps the worktree for debugging.
