# Hosts and sessions

> **Owns:** host config, task placement, how sessions are started and reached. **Not here:** what a session does (the skills, `plugins/engine/skills/`), what it records (`telemetry.md`), each harness's launch line, tiers, permissions and apps (`claude.md`, `codex.md`).

## Hosts

For server dispatch, ordinary Git credentials, isolated clones and execution
credential files, load `control-server.md`.

Each host declares its harnesses, session capacity and capabilities. Declare capabilities explicitly.

**One file, every host.** The host config is `~/.config/engine/hosts.toml`,
the same file on every host, because any host can run the coordinator. Edit
it on one host; `engine setup --host <name>` copies it to another. Each host also keeps its own name,
one line, in `~/.config/engine/host`.

**Names and addresses.** A host's name is its `[host.<name>]` key, a logical
name such as `mac` or `devbox`, never a hostname or an IP. It is what attempts
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
harness_ratio = { claude = 4, codex = 1 }
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
launch line expands to it. Engine never runs whichever comes first on
`PATH`.

## Placement

In start order, the coordinator gives each ready task a host that meets all
of these, preferring the most free slots (heavy ones, for a heavy task),
then the fewest capabilities, then the lowest name:

- a free slot, from the host's `status` (a host whose `status` fails takes
  no task), and a free heavy slot too for a heavy task;
- the chosen harness, under its cap across hosts;
- every capability in the card's `requires`.

For each unpinned card, `harness_ratio` scores positive-weight harnesses by
`(live sessions across hosts + 1) / weight`. Lowest wins; ties prefer
`default_harness`, then name. If no host fits, try the next harness. Counts
include pinned cards and every live session. Zero
weights exclude unpinned starts. No ratio: `default_harness`.
Unknown harnesses and all-zero ratios are refused.

An unplaced task stays ready, noticed each pass. If none could take it with
all slots free, the coordinator notifies the operator once. The planner
starts at `deep` on the plan's harness, else `default_harness`. For host
calls, load `coordinator.md`.

## Naming

Every surface uses one name per session, `<repo>-<task>`, such as
`boga3-412`: the tmux window, the remote-control name and part of the
branch. It holds letters, digits, `_`, `.` and `-`, and does not start with
`-`: tmux treats anything else as special.

## Starting a session

The coordinator dispatches a task's session on the host placement picks
(`control-server.md`, "Dispatch and recovery"). It decides the model, effort
and kickoff from the task's tier, then has the host run its own `sync-repo`
and `spawn` ops (`coordinator.md`, "Reaching hosts"), in-process for this
host, over SSH for another. The host's `spawn` refuses when the host file,
`hosts.toml` or this host's entry is missing; the harness is unknown or not
run on the host, or its binary is not executable; its window exists; or no
slot is free, or no heavy slot for a heavy task. The coordinator itself
refuses a host that lacks a capability in the task's `requires`. An
unreachable host is an error, not a refusal. A host on another protocol is
refused, naming `engine setup --host <name>` (`operator-setup.md`). A
harness's cap across hosts is placement's alone.

The host:

1. syncs engine's clone using normal Git configuration, clearing matching
   engine-generated local credential and identity overrides; clones it over
   HTTPS on first use, fetches, and leaves it at the remote's default branch;
2. creates the worktree on `task/<repo>-<task>`, with the repo's own
   `worktree create` when it provides one (`project-context.md`,
   "Worktrees"), else `git worktree add` from the default branch, refusing
   a branch on `origin`, or a local one with commits (one without is
   remade). A dispatch marked a resume opens on `origin`'s branch
   instead, at its tip, refusing a local branch with commits `origin` lacks.
   A worktree left there is adopted, fast-forwarded, when clean with no
   commits `origin` lacks, else refused;
3. makes the worktree trusted by its harness, so no trust dialog blocks the
   start (`claude.md` and `codex.md`, "Starting");
4. opens the window `<repo>-<task>` in the session `engine`, in the worktree,
   so the repo's `AGENTS.md` loads;
5. runs the host's launch line at the task's tier (default `standard`), with
   the flags the harness adds at start (the same sections). Then `--` and
   the kickoff, the session's first message, which names the engine skill's
   server executor or planner mode, the task or plan, the execution and the
   reviewer model. `/bin/sh` exports `ENGINE_RUN_DIR` (the run directory),
   `ENGINE_HARNESS` (the harness), the server's URL, credential file and the
   attempt, and `ENGINE_UNATTENDED=1` in hands-off mode. A Codex session
   joined to remote control gets the `ENGINE_*` variables on its command line
   instead (`codex.md`, "Starting");
6. writes `run.json` (`telemetry.md`; a Codex session joined to remote
   control gets it before step 4, since its hooks find their run by it).

**Preflight:** `engine start --check [--harness <name>] [--host <name>]` (Claude
without `--harness`) runs `gh auth status` in the `engine` tmux server
(`operator-setup.md`, "GitHub access"); a hands-off light-tier session,
`engine-check`, reads the skill and a file it wrote in `/tmp`, runs `gh` and
`engine`, and closes its window. It exits 1, printing what it met, when gh
failed or the session was denied, stalled or skipped a step. A Codex check
also needs the hooks to have written its state file (`codex.md`).

**Other ways to start:** a session started any other way runs the same
skill, creates its own worktree, and is reachable only where it was started.

## Seeing sessions

| Where | How |
| --- | --- |
| Phone or browser | The dashboard (`engine ui`, `control-server.md`) |
| Terminal | `engine ps [--json]`: every server task, with its state and attempt. The dashboard's session view adds each attempt's host and observed state (`working`, `waiting`, `limited`, `live`, `exited` or `unknown`). The signals are in `coordinator.md`. |

## Connecting to a session

| From | How |
| --- | --- |
| Any terminal | `engine attach <task-id>`: `tmux attach` on its host, locally, over `ssh -t <address>`, or `switch-client` inside tmux. |
| Phone, SSH | An SSH app over Tailscale, `tmux attach -t engine`, then `Ctrl-b w`. |
| Apps | Claude: the Claude apps and claude.ai/code, through Remote Control. Codex: the ChatGPT and Codex apps, when the session joined engine's remote-control daemon (`codex.md`, "Apps"). |

For app setup changes, load `claude.md` or `codex.md`.

## tmux setup

Engine owns the tmux session `engine` on each host. It sets its options on
the session and on each window it opens, and never edits the host's
`~/.tmux.conf` or tmux's global options. tmux reads `.` as a pane separator,
so engine targets the windows it opens by id (`@3`), not by name. Windows stay after their command exits
(`remain-on-exit`), so a dead session's last screen and exit stay readable.

When engine has to type into a session, it sends the text with `send-keys -l`,
then `Enter` as a separate call after a short delay. Input boxes can swallow a
newline that arrives with the text. The coordinator does this over SSH for a session on another host.

## Keeping hosts awake

tmux survives a closed terminal, but not a sleeping machine. A macOS
host holds off sleep per window, while on power. A Linux host should not suspend at all.

## Lifecycle

- **Questions:** the executor asks with `engine ask`, which sets `needs-human`,
  and waits in the live session. Answer from any surface. A reply in the
  operator inbox also works, because the executor reads its updates when it
  resumes.
- **End:** after cleanup, a hands-off executor closes its own window; a gated
  one leaves it for the operator.
- **Crash:** the window has died but the task is still `running`. The
  coordinator marks it `failed` (or resumes it, `control-server.md`),
  keeping the worktree.
