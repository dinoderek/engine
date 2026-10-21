# Hosts and sessions

> **Owns:** host config, task placement, how sessions are started and reached. **Not here:** what a session does (the executor skill, `plugins/engine/skills/executor/SKILL.md`), what it records (`telemetry.md`).

## Hosts

A host is somewhere sessions run. Each one declares the harnesses it runs, how
many sessions it holds, and its capabilities. Capabilities are declared, never inferred.

**One file, every host.** The host config is `~/.config/engine/hosts.toml`,
the same file on every host, because any host can run the coordinator. Edit
it on one host and copy it to the others. Each host also keeps its own name,
one line, in `~/.config/engine/host`.

**Names and addresses.** A host's name is its `[host.<name>]` key, a logical
name such as `mac` or `devbox`, never a hostname or an IP. It is what claims
and every surface show. Its `address` is how other hosts reach it over SSH
(its Tailscale name); it stays in this file and is never posted anywhere. A
machine (below) is local to whichever host has its name in `~/.config/engine/host`,
and reached over SSH at its `address` from every other one.

**Machines and cloud hosts.** A host with no `kind` is a machine: it needs
`address` and `ram_gb`. `kind = "cloud"` is the only kind; a cloud host has no
address or RAM and must set `max_sessions`.

**Slots from memory.** RAM stands in for how many sessions a machine runs at
once: `(ram_gb - reserve_gb) / gb_per_session`, rounded down. Both values
are set at the top and may be set again per host. A host's `max_sessions`
replaces the derived count.

**Capabilities:**

| Capability | Means |
| --- | --- |
| `macos` | runs macOS |
| `linux` | runs Linux |
| `ios` | builds iOS apps and runs the iOS Simulator (Xcode) |
| `vm` | runs virtual machines |
| `docker` | runs Docker containers |

A card's `requires` names capabilities from this table. A new one is added
here first, then declared by the hosts that have it.

```toml
default_harness = "claude"
gb_per_session = 8      # RAM one session and its gates need
reserve_gb = 8          # RAM kept back for the host itself

[harness.claude]
max_sessions = 8        # across all hosts: a budget lever
launch = "claude --permission-mode auto --model {model}"
[harness.codex]
max_sessions = 2
launch = "codex --sandbox workspace-write -a on-request -c approvals_reviewer=auto_review -c sandbox_workspace_write.network_access=true -c 'sandbox_workspace_write.writable_roots=[\"{clone}\"]' -m {model}"

[host.mac]              # 4 slots
address = "<tailscale name>"
harnesses = ["claude", "codex"]
ram_gb = 48
reserve_gb = 16         # also the operator's workstation
capabilities = ["macos", "ios", "vm", "docker"]

[host.devbox]           # 3 slots
address = "<tailscale name>"
harnesses = ["claude", "codex"]
ram_gb = 32
capabilities = ["linux", "vm", "docker"]

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

- the host is enabled and has a free slot (its slots are under "Hosts");
- the host lists the harness (the card's, or the default), and that harness
  is under its own cap;
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

Every surface uses one name per session, `<repo>-<task>`, for example
`boga3-412`. It is the tmux window, the remote-control session name and part
of the branch name, so a session can be found from any surface. Never use `#` in
the name: tmux treats it as special.

## Starting a session: `engine start`

`engine start <owner/repo#n> [--host H] [--harness claude|codex] [--model M]`
runs from any terminal, in any directory. It returns as soon as the session
is up. On the target host (this machine, unless `--host` says otherwise) it:

1. syncs engine's clone of the repo;
2. creates the task worktree with the repo's own command;
3. opens a window `<repo>-<task>` in the tmux session `engine`, in that
   worktree, so the repo's `AGENTS.md` loads;
4. launches the harness's interactive CLI with the host's launch line, and
   the kickoff prompt as its first message (`Execute <issue> with the engine
   executor, hands-off mode`), plus the `run=`, `tmux=` and `rc=` values its
   claim comment records;
5. makes the session reachable from the apps (see below);
6. prints how to attach.

The executor claims the task itself. `engine start` only places and launches.

**Remote hosts:** with `--host devbox`, every step runs on `devbox` over SSH
(`ssh <address> engine host spawn …`). The session lives in `devbox`'s tmux
server, so the machine you typed on can sleep or disconnect.

**Other ways to start:** a session started any other way (Claude desktop, a
plain `claude` in a terminal) runs the same executor. It creates its own
worktree, and it shows up wherever its claim comment does. It can only be
reached from wherever it was started.

## The claim comment is the registry

The executor's claim comment on the task issue records the run ID, the
host's logical name, the harness and model, the mode, the tmux target, the
Remote Control name and the branch. The executor skill owns its format.

Everything that lists sessions reads these comments, so a task started by
hand and one started by the coordinator look the same.

## Seeing sessions

| Where | How |
| --- | --- |
| Phone or browser | Saved GitHub searches on `engine:running`, `engine:needs-human` and `engine:ruling-review` |
| Terminal | `engine ps`: every claimed task, with its host, harness, state (working, waiting for input, needs-human, in review, CI) and age. Liveness comes from probing each host's tmux; state comes from the hook signals (`coordinator.md`). |

## Connecting to a session

| From | How |
| --- | --- |
| Any terminal | `engine attach <repo>-<task>`. This runs `tmux attach` locally, or `ssh <address> -t tmux attach -t engine:<repo>-<task>` for a remote host. Detach with `Ctrl-b d`; the session keeps running. |
| Phone, SSH | An SSH app over Tailscale, then `tmux attach -t engine`, then `Ctrl-b w` to pick the window. Best for debugging. |
| Phone, chat | Claude sessions: the Claude app, through Remote Control. Codex sessions: the ChatGPT app, through Codex remote control. Best for answering a question. |
| Desktop | claude.ai/code or the Claude desktop app for Remote Control sessions; the ChatGPT or Codex desktop app for Codex. |

Several clients can attach to one window at once. Everyone sees the same
screen, and anyone can type.

Each harness reaches the apps differently:

- **Claude:** the session has Remote Control turned on.
- **Codex:** the host runs the remote-control app-server
  (`codex remote-control start`), and the session's TUI connects to it with
  `--remote`.

## tmux setup

Engine owns the tmux session `engine` on each host. It sets that session's
options itself and never edits the host's `~/.tmux.conf`:

- `mouse on` and `history-limit 50000`;
- `allow-rename off` and `automatic-rename off`, so agents can't rename
  windows;
- `window-size latest`, so the window fits whoever attached last (a phone or
  a laptop);
- `remain-on-exit on`, so a crashed session's last screen stays readable.

`pipe-pane` copies each window's output into its run directory, so the raw log
outlives the tmux server. When engine has to type into a session, it sends the
text with `send-keys -l`, then `Enter` as a separate call after a short delay.
Input boxes can swallow a newline that arrives with the text.

## Keeping hosts awake

tmux survives a closed terminal, but not a sleeping machine. While it has live
sessions, a macOS host runs under `caffeinate -s`, which holds off sleep while
it is on power. A Linux host should not suspend at all.

## Lifecycle

- **Questions:** the executor sets `needs-human` and waits in the live session.
  Answer from any surface. A comment on the task's issue also works, because
  the executor reads its inbox when it resumes.
- **End:** after cleanup, the executor exits and engine closes the window.
- **Crash:** the window has died but the task is still `running`. `engine ps`
  shows it as dead. The coordinator, when one is running, marks it `failed`
  and keeps the worktree for debugging.
