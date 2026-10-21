# Hosts and sessions

> **Owns:** host config, task placement, how sessions are started and reached. **Not here:** what a session does (the `engine` skill, `plugins/engine/skills/engine/`), what it records (`telemetry.md`).

## Hosts

A host is somewhere sessions run. Each one declares the harnesses it runs, how
many sessions it holds, and its capabilities. Capabilities are declared, never inferred.

**One file, every host.** The host config is `~/.config/engine/hosts.toml`,
the same file on every host, because any host can run the coordinator. Edit
it on one host and copy it to the others. Each host also keeps its own name,
one line, in `~/.config/engine/host`.

**Names and addresses.** A host's name is its `[host.<name>]` key, a logical
name such as `mac` or `devbox`, never a hostname or an IP. It is what claims
and every surface show. Its `address` is how other hosts reach it over plain
SSH, `<user>@<tailscale name>`, passed to `ssh` as is, so it needs no
`~/.ssh/config` entry. The address stays in this file and is never posted
anywhere. A machine (below) is local to whichever host has its name in
`~/.config/engine/host`, and reached over SSH at its `address` from every
other one.

**Machines only.** Every host is a machine, this one or one reached over SSH,
and needs `address` and `ram_gb`. Engine does not spawn cloud sessions.

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
launch = "{bin} --permission-mode auto --model {model} --effort {effort} -n {name} --remote-control {name}"
tiers.deep     = { model = "claude-opus-5-5",   effort = "high" }
tiers.standard = { model = "claude-sonnet-5-5", effort = "high" }
tiers.light    = { model = "claude-haiku-5-5",  effort = "medium" }

[harness.codex]
max_sessions = 2
launch = "{bin} --sandbox workspace-write -a on-request -c approvals_reviewer=auto_review -c sandbox_workspace_write.network_access=true -c 'sandbox_workspace_write.writable_roots=[\"{clone}\"]' -m {model} -c model_reasoning_effort={effort}"
tiers.deep     = { model = "gpt-6.1-sol", effort = "high" }
tiers.standard = { model = "gpt-6.1-sol", effort = "medium" }
tiers.light    = { model = "gpt-6-luna",  effort = "medium" }

[host.mac]              # 4 slots
address = "<user>@<tailscale name>"
harnesses = ["claude", "codex"]
ram_gb = 48
reserve_gb = 16         # also the operator's workstation
capabilities = ["macos", "ios", "vm", "docker"]
bin = { claude = "~/.local/bin/claude", codex = "~/.local/bin/codex" }

[host.devbox]           # 3 slots
address = "<user>@<tailscale name>"
harnesses = ["claude", "codex"]
ram_gb = 32
capabilities = ["linux", "vm", "docker"]
bin = { claude = "~/.local/bin/claude", codex = "~/.local/bin/codex" }
```

Each harness maps every tier (`execution-protocol.md`, "Models") to a model
and an effort level. GPT-6 Astra is deliberately unmapped: GPT-6.1 Sol at high
effort comes close to it on coding at a fraction of the cost.

Each host pins the path of every harness binary (`bin`), and `{bin}` in the
launch line expands to it. Engine never runs whichever install comes first on
`PATH`: this Mac has a second Codex under Homebrew that shadows the standalone
one. The other placeholders: `{name}` is the session name (`<repo>-<task>`),
`{model}` and `{effort}` come from the tier, and `{clone}` is engine's clone of
the repo.

**Validation.** `internal/hosts` reads the file with a strict parser for the
TOML subset above: tables, dotted keys, single-line strings, integers, arrays
of strings, one-line inline tables and comments. It rejects anything else, an
unknown key, a duplicate, and any broken rule on this page, and reports the
first violation with its line number. A launch line must start with `{bin}`,
contain `{model}` and use only the placeholders listed here. A host's `bin`
names exactly the harnesses it runs. Every tier is mapped for every harness.

## Placement

The coordinator places a ready task on a host that meets all of these:

- the host has a free slot (its slots are under "Hosts");
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
of the branch name, so a session can be found from any surface. The name
holds letters, digits, `_`, `.` and `-`, and does not start with `-`: tmux
treats anything else (`#`, `:`, spaces) as special.

## Starting a session: `engine start`

`engine start <owner/repo#n> [--tier deep|standard|light] [--mode hands-off|gated]`
launches a Claude session on this machine from any directory, and returns
once it is up. `--mode` defaults to `hands-off`; `--tier` replaces the card's.

It first refuses, changing nothing, when: the token file, host file or
`hosts.toml` is missing or lacks this host; the task is closed, has a task
state label or an open blocker, or is standalone without `engine:approved`;
the card's harness is unknown (a misspelt tier reads as one), not Claude, or
not run here; its window exists; or no slot is free (a window whose command
runs holds one). Then it:

1. syncs engine's clone (`operator-setup.md`, "Machine user"): clones it over
   HTTPS on first use, fetches, and leaves it at the remote's default branch;
2. creates the worktree on `task/<repo>-<task>`, with the clone's executable
   `./dev worktree create` if it has one, else `git worktree add` from the
   default branch, refusing a branch that exists locally or on `origin`;
3. sets `projects.<worktree>.hasTrustDialogAccepted` in `~/.claude.json` (or
   `$CLAUDE_CONFIG_DIR`'s), so Claude's trust dialog, whose default answer
   exits, never shows;
4. opens the window `<repo>-<task>` in the session `engine`, in the worktree,
   so the repo's `AGENTS.md` loads;
5. runs the host's launch line at the card's tier (default `standard`), the
   kickoff as its last argument and Claude's first message: `Execute
   <owner/repo#n> with the engine executor, <mode> mode. Reviewer model:
   <deep model>. run=<run-id> tmux=engine:<name> rc=<name>`, values the
   claim records. The shell first exports `GH_TOKEN` from the token file,
   keeping it off every command line;
6. writes `run.json` (`telemetry.md`) and prints how to reach the session.

The executor claims the task; its claim check stops a racing second start.
Designed, not built: `--harness codex`, and `--host` (spawning over SSH).

**Other ways to start:** a session started any other way (Claude desktop, a
plain `claude` in a terminal) runs the same executor. It creates its own
worktree, and it shows up wherever its claim comment does. It can only be
reached from wherever it was started.

## The claim comment is the registry

The executor's claim comment on the task issue records the run ID, the
host's logical name, the harness and model, the mode, the tmux target, the
Remote Control name and the branch. The `engine` skill owns its format.

Everything that lists sessions reads these comments, so a task started by
hand and one started by the coordinator look the same.

## Seeing sessions

| Where | How |
| --- | --- |
| Phone or browser | Saved GitHub searches on `engine:running`, `engine:needs-human` and `engine:ruling-review` |
| Terminal | `engine ps`: every claimed task, with its host, harness, state (working, waiting for input, needs-human, in review, CI) and age. Liveness comes from probing each host's tmux; state comes from the labels and the harness signals (`coordinator.md`). |

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

- **Claude:** the launch line's `-n {name} --remote-control {name}` sets the
  session's name, `<repo>-<task>`, and shows it in the apps under that name.
  Remote Control needs the host signed in with a claude.ai account
  (`operator-setup.md`).
- **Codex:** the host runs the remote-control app-server
  (`codex remote-control start`), and the session's TUI connects to it with
  `--remote`.

## tmux setup

Engine owns the tmux session `engine` on each host, and creates it with its
first window. It sets the options itself, on the session and on each window it
opens, and never edits the host's `~/.tmux.conf` or tmux's global options:

- on the session: `mouse on` and `history-limit 50000`;
- on each window: `allow-rename off` and `automatic-rename off`, so agents
  can't rename windows;
- on each window: `window-size latest`, so it fits whoever attached last (a
  phone or a laptop);
- on each window: `remain-on-exit on`, so a crashed session's last screen
  stays readable.

`pipe-pane` copies each window's output into its run directory, so the raw log
outlives the tmux server. Engine opens a window on a placeholder command, sets
the options and the pipe, then swaps in the task's command, so a command that
fails at once still leaves its log and its screen. tmux reads `.` as a pane
separator, so engine targets the windows it opens by id (`@3`), not by name.

When engine has to type into a session, it sends the text with `send-keys -l`,
then `Enter` as a separate call after a short delay. Input boxes can swallow a
newline that arrives with the text.

## Keeping hosts awake

tmux survives a closed terminal, but not a sleeping machine. On a macOS host,
each window that engine opens starts `caffeinate -s -w <pid>` on its command's
process, through tmux's `run-shell`. `-s` holds off sleep while on power, and
`-w` ends it when the command does, so nothing outlives the last command. A
Linux host should not suspend at all.

## Lifecycle

- **Questions:** the executor sets `needs-human` and waits in the live session.
  Answer from any surface. A comment on the task's issue also works, because
  the executor reads its inbox when it resumes.
- **End:** after cleanup, the executor exits and engine closes the window.
- **Crash:** the window has died but the task is still `running`. `engine ps`
  shows it as dead. The coordinator, when one is running, marks it `failed`
  and keeps the worktree for debugging.
