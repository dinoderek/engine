# Hosts and sessions

> **Owns:** host config, task placement, how sessions are started and reached. **Not here:** what a session does (the `engine` skill, `plugins/engine/skills/engine/`), what it records (`telemetry.md`), each harness's launch line, tiers, permissions and apps (`claude.md`, `codex.md`).

## Hosts

A host is somewhere sessions run. Each one declares the harnesses it runs, how
many sessions it holds, and its capabilities. Capabilities are declared, never inferred.

**One file, every host.** The host config is `~/.config/engine/hosts.toml`,
the same file on every host, because any host can run the coordinator. Edit
it on one host; `engine setup --host <name>` copies it to another. Each host also keeps its own name,
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
replaces the derived count. Only a live window with a run recorded under
its name takes a slot: an executor's or the planner's. A coordinator's
window or a stray shell takes none.

**Heavy sessions.** A task is heavy when its card requires one of the
top-level `heavy_capabilities`: a simulator or a VM loads a machine far more
than a session does. A host's `max_heavy_sessions` caps its heavy sessions
below its slots; without it, every slot may be heavy, and a cap above the
slots is refused. `run.json` records whether a run is heavy, and `status`
reports `heavy_slots` and `free_heavy_slots`, never more than `free_slots`.

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
heavy_capabilities = ["ios", "vm"]

[host.mac]              # 4 slots
address = "<user>@<tailscale name>"
harnesses = ["claude", "codex"]
ram_gb = 48
reserve_gb = 16         # also the operator's workstation
max_heavy_sessions = 2  # at most 2 of its 4 sessions heavy
capabilities = ["macos", "ios", "vm", "docker"]
bin = { claude = "~/.local/bin/claude", codex = "~/.local/bin/codex" }

[host.devbox]           # 3 slots
address = "<user>@<tailscale name>"
harnesses = ["claude", "codex"]
ram_gb = 32
capabilities = ["linux", "vm", "docker"]
bin = { claude = "~/.local/bin/claude", codex = "~/.local/bin/codex" }
```

Each harness's launch line and tier models are in its own doc, `claude.md`
and `codex.md`. Each harness maps every tier (`execution-protocol.md`, "Models")
to a model and an effort level.

Each host pins the path of every harness binary (`bin`), and `{bin}` in the
launch line expands to it. Engine never runs whichever install comes first on
`PATH`: this Mac has a second Codex under Homebrew that shadows the standalone
one.

## Placement

In start order, the coordinator gives each ready task a host that meets all
of these, preferring the most free slots (heavy ones, for a heavy task),
then the fewest capabilities, then the lowest name:

- a free slot, from the host's `status` (a host whose `status` fails takes
  no task), and a free heavy slot too for a heavy task;
- the task's harness (the card's, or the default), under its cap across
  hosts;
- every capability in the card's `requires`.

An unplaced task stays ready, logged with the reason each pass. If no host
could take it with every slot free, the coordinator comments the reason on
it once. The planner starts on the coordinator's host. How the coordinator
reaches each host is in `coordinator.md`.

## Naming

Every surface uses one name per session, `<repo>-<task>`, such as
`boga3-412`: the tmux window, the remote-control name and part of the
branch. It holds letters, digits, `_`, `.` and `-`, and does not start with
`-`: tmux treats anything else as special.

## Starting a session: `engine start`

`engine start <owner/repo#n> [--harness <name>] [--tier deep|standard|light] [--mode hands-off|gated] [--host <name>]`
launches a session from any directory, and returns once it is up.
`--mode` defaults to `hands-off`; `--harness` and `--tier` replace the card's. With no
`--host` the session runs on this machine; `--host` names another machine in
`hosts.toml` and starts it there over SSH.

It first refuses, changing nothing, when: the token file, host file,
`hosts.toml` or this host's entry is missing; the task is closed, has a task
state label or an open blocker, or is standalone without `engine:approved`;
the harness (`--harness`, else the card's, else the default) is unknown (as a
misspelt tier is) or not run on the host, or its binary is not executable;
the host lacks a capability in the card's `## Requires`, even when named; its
window exists; or no slot is free, or no heavy slot for a heavy task. Then it:

1. syncs engine's clone (`operator-setup.md`, "Machine user"): clones it over
   HTTPS on first use, fetches, and leaves it at the remote's default branch;
2. creates the worktree on `task/<repo>-<task>`, with the clone's executable
   `./dev worktree create` if it has one, else `git worktree add` from the
   default branch, refusing a branch on `origin`, or a local one with
   commits (one without is remade);
3. makes the worktree trusted by its harness, so no trust dialog blocks the
   start (`claude.md` and `codex.md`, "Starting");
4. opens the window `<repo>-<task>` in the session `engine`, in the worktree,
   so the repo's `AGENTS.md` loads;
5. runs the host's launch line at the card's tier (default `standard`), with
   the flags the harness adds at start (the same sections). Then `--` and
   the kickoff, the session's first message: `Execute <owner/repo#n> with the
   engine executor, <mode> mode. Reviewer model: <deep model>. run=<run-id>
   tmux=engine:<name> rc=<name>` (`rc=none` for Codex, which has no Remote
   Control session). `/bin/sh` first exports `GH_TOKEN` from the
   token file, off every command line, `ENGINE_RUN_DIR` (the run directory),
   `ENGINE_HARNESS` (the harness), and `ENGINE_UNATTENDED=1` in hands-off
   mode;
6. writes `run.json` (`telemetry.md`) and prints how to attach.

The checks run in `engine start`, which also picks the model, effort and
kickoff. Steps 1 to 6, all but printing how to attach, are the host's own
`sync-repo` and `spawn` ops (`coordinator.md`, "Reaching hosts"), which
`engine start` calls in-process, or over SSH with `--host`.
The executor claims the task; its claim check stops a racing second start.
With `--host`, the caller makes the task, harness, tier and `requires`
refusals, and reads the window and free slots, heavy ones too, from the
host's `status`; the host's own `spawn` refuses anything else, such as a
missing harness binary.
An unreachable host is an error, not a refusal. A host on another protocol
is refused, naming `engine setup --host <name>` (`operator-setup.md`). A
harness's cap across hosts is placement's alone, so `engine start` does not
check it.

**Preflight:** `engine start --check [--harness <name>] [--host <name>]` (Claude
without `--harness`) opens a throwaway hands-off session,
`engine-check`, at the light tier, that reads the skill, reads a file it
wrote in `/tmp`, runs `gh` and `engine`, and closes its window. It exits 1,
printing what it met, when the session had a permission request denied,
stalled or skipped a step. A Codex check also needs the session's hooks to
have written its state file (`codex.md`).

**Other ways to start:** a session started any other way runs the same
executor, creates its own worktree, and is reachable only where it was
started.

## The claim comment is the registry

The executor's claim comment on the task issue records where and how its
session runs. The `engine` skill owns its format.

Everything that lists sessions reads these comments, so a task started by
hand and one started by the coordinator look the same.

## Seeing sessions

| Where | How |
| --- | --- |
| Phone or browser | Saved GitHub searches on `engine:running`, `engine:needs-human` and `engine:ruling-review` |
| Terminal | `engine ps [--json]`: every claimed task, with its host, harness, state (`working`, `waiting`, `needs-human`, `in-review`, `running` or `dead`) and age. `--json` prints an array. Each claim is settled from the `status` of the host it names, this one's read in process, one call per host with a claim, in parallel; if the call fails the row keeps its label state with liveness `null` and `ps` warns. The signals are in `coordinator.md`. |

## Connecting to a session

| From | How |
| --- | --- |
| Any terminal | `engine attach <repo>-<task>`: `tmux attach` on its host, locally, over `ssh -t <address>`, or `switch-client` inside tmux. |
| Phone, SSH | An SSH app over Tailscale, `tmux attach -t engine`, then `Ctrl-b w`. |
| Apps | Claude: the Claude apps and claude.ai/code, through Remote Control. Codex: none; a session engine starts runs embedded (`codex.md`, "Apps"). |

Clients share one window: everyone sees the same screen and can type.

Each harness's apps are set up in its own doc: `claude.md` and `codex.md`.

## tmux setup

Engine owns the tmux session `engine` on each host. It sets its options on
the session and on each window it opens, and never edits the host's
`~/.tmux.conf` or tmux's global options. tmux reads `.` as a pane separator,
so engine targets the windows it opens by id (`@3`), not by name. Windows stay after their command exits
(`remain-on-exit`), so a dead session's last screen and exit stay readable.

When engine has to type into a session, it sends the text with `send-keys -l`,
then `Enter` as a separate call after a short delay. Input boxes can swallow a
newline that arrives with the text. `engine tell` does this over SSH for a session on another host.

## Keeping hosts awake

tmux survives a closed terminal, but not a sleeping machine. A macOS
host holds off sleep per window, while on power. A Linux host should not suspend at all.

## Lifecycle

- **Questions:** the executor sets `needs-human` and waits in the live session.
  Answer from any surface. A comment on the task's issue also works, because
  the executor reads its inbox when it resumes.
- **End:** after cleanup, a hands-off executor closes its own window; a gated
  one leaves it for the operator.
- **Crash:** the window has died but the task is still `running`. `engine ps`
  shows it dead; a running coordinator marks it `failed`, keeping the worktree.
