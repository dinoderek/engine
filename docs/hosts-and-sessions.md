# Hosts and sessions

> **Owns:** host config, task placement, how sessions are started and reached. **Not here:** what a session does (`execution-protocol.md`), what it records (`telemetry.md`).

## Hosts

A host is somewhere sessions run. Each one declares the harnesses it runs, how
many sessions it holds at once, and its capabilities. Capabilities are
declared, never inferred.

```toml
default_harness = "claude"

[harness.claude]
max_sessions = 8        # across all hosts: a budget lever
launch = "claude --permission-mode <hands-off mode> --model {model}"
[harness.codex]
max_sessions = 2
launch = "codex --sandbox <mode> --ask-for-approval never -m {model}"

[host.mac]
kind = "local"
harnesses = ["claude", "codex"]
max_sessions = 3
capabilities = ["ios", "android", "docker"]

[host.linux]
kind = "local"
harnesses = ["claude", "codex"]
max_sessions = 6
capabilities = ["docker"]

[host.claude-cloud]
kind = "cloud"
harnesses = ["claude"]
max_sessions = 4
capabilities = []
enabled = false         # until spawning is verified

[host.codex-cloud]
kind = "cloud"
harnesses = ["codex"]
max_sessions = 2
capabilities = []
enabled = false
```

## Placement

The coordinator places a ready task on a host that meets all of these:

- the host is enabled and has a free slot;
- the harness (the card's, or the default) is under its own cap;
- the host's capabilities include everything in the card's `requires`.

Ties go to the host with the most free slots. A task that no host can take
stays ready, and the coordinator reports why. Each harness's launch line must
start a session that never stops for a permission prompt; only hard stops
should pause it. How the coordinator reaches each host is in `coordinator.md`.

## Naming

Every surface uses one name per session, `<repo>#<task>`, for example
`boga3#412`. It is the tmux window, the remote-control session name and the
branch prefix. So a session is findable from whichever surface you are on.

## Reaching a session

| Session | Phone | Desktop | SSH / tmux |
| --- | --- | --- | --- |
| Claude CLI, local host | Claude app, via Remote Control | claude.ai/code, Claude desktop | yes |
| Codex CLI, local host | ChatGPT app, via Codex Remote (CLI pairing is experimental) | ChatGPT / Codex desktop | yes |
| Claude cloud | Claude app | claude.ai/code | no |
| Codex cloud | ChatGPT app | ChatGPT / Codex web | no |

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
  with the model and the kickoff prompt (`Execute task <tracker ref> with the
  engine executor protocol`), and register remote control.
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
