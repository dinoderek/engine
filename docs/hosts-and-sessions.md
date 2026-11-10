# Hosts and sessions

> **Owns:** host config, task placement, the attempt as the session registry, how sessions are started and reached. **Not here:** dispatch, host ops and session signals (`coordinator.md`), what a session does (the skills, `plugins/engine/skills/`), what it records (`telemetry.md`), each harness's launch line, tiers and adapter (`harnesses.md`).

## Hosts

For dispatch, Git credentials and run credential files, load `coordinator.md`.
The fields of `hosts.toml` are in `internal/hosts/hosts.go`.

**One file, every host.** `~/.config/engine/hosts.toml` is the same on every
host: edit it on one, and `engine setup --host <name>` copies it. Each host
keeps its own name in `~/.config/engine/host`.

**Names and addresses.** A host's name is its `[host.<name>]` key: a logical
name such as `mac`, never a hostname or an IP. Its `address`
(`<user>@<tailscale name>`) is passed to `ssh` as is, and is never posted
anywhere. A host is local to the machine whose `~/.config/engine/host` names
it, and reached over SSH from every other one.

**Slots.** `(ram_gb - reserve_gb) / gb_per_session`, rounded down, is a
machine's concurrent sessions. The top-level values set the defaults and a
host overrides them; `max_sessions` replaces the derived count. Only a live
window with a run recorded under its name (an executor's or the planner's)
takes a slot.

**Heavy sessions.** A task is heavy when its card requires one of the
top-level `heavy_capabilities`, such as a simulator or VM. A host's
`max_heavy_sessions` caps them below its slots; without it every slot may be
heavy. `status` reports `heavy_slots` and `free_heavy_slots`.

**Capabilities** are declared per host and named by a card's `requires`.
`ios` means iOS builds and the Simulator.

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

Each host pins every harness binary (`bin`), which `{bin}` in the launch line
expands to. Engine never uses whichever binary comes first on `PATH`.
Launch lines and tier models are in `harnesses.md`.

## Placement

In start order, each ready task gets a host that meets all of these, preferring
the most free slots (heavy ones, for a heavy task), then the fewest
capabilities, then the lowest name:

- a free slot from the host's `status` (none if its `status` fails), and a
  free heavy slot for a heavy task;
- the chosen harness, under its cap across hosts, and signed in there;
- every capability in the card's `requires`.

For each unpinned card, `harness_ratio` scores positive-weight harnesses by
`(live sessions across hosts + 1) / weight`, counting pinned cards and every
live session. Lowest wins; ties prefer `default_harness`, then name. If no
host fits, the next harness is tried. Zero weights exclude a harness from
unpinned starts. With no ratio, `default_harness` is used.

An unplaced task stays ready and waits; the operator is notified once if no
host could take it even with all slots free. The planner starts at `deep` on
the plan's harness, else `default_harness`, once signed in there.

## Attempts and naming

An attempt is the session registry: the server's record of a task's or
plan's session (host, harness, run ID, tmux window, state, timestamps;
`engine attempt list [<task-id>]`). `engine ps`, `engine attach`
and the dashboard resolve a session through its current attempt.

A session is named `<repo>-<task>`, such as `boga3-412`, on every surface: the
tmux window, the remote-control name and part of the branch. It holds
letters, digits, `_`, `.` and `-`, and never starts with `-`.

## Starting a session

The coordinator dispatches a session on the host placement picks
(`coordinator.md`), with the model, effort and kickoff its tier gives. The
host runs `control-spawn`, which syncs engine's clone (over HTTPS on first
use) and then:

1. creates the worktree on `task/<repo>-<task>`, with the repo's own
   `worktree create` when it provides one (`project-context.md`,
   "Worktrees"), else `git worktree add` from the default branch. A resume
   opens on `origin`'s branch at its tip; a clean worktree left there with no
   commits `origin` lacks is adopted and fast-forwarded, else refused;
2. trusts the worktree for its harness;
3. opens the window `<repo>-<task>` in the session `engine`, in the worktree;
4. runs the host's launch line at the task's tier (default `standard`) with
   the flags the harness's adapter adds, then `--` and the kickoff: the
   session's first message, naming the `engine` skill's server executor or
   planner mode, the task or plan, the attempt and the reviewer model.
   `/bin/sh` exports `ENGINE_RUN_DIR`, `ENGINE_HARNESS`, the server's URL,
   credential file and attempt, and `ENGINE_UNATTENDED=1` when hands-off. A
   session that joins a remote-control daemon gets them from its adapter;
5. writes `run.json` (`telemetry.md`): before step 3 for a session that joins
   a daemon, since its hooks find their run by it.

Spawn refuses a missing host entry or host file, an unknown or unrunnable
harness, or an existing window. Slots, capabilities and the harness cap are
placement's alone. A host on another protocol fails, naming
`engine setup --host <name>` (`operator-setup.md`).

**Preflight:** `engine start --check [--harness <name>] [--host <name>]`
runs `gh auth status` in the `engine` tmux server, then a hands-off light-tier
session that reads the skill, file I/O in `/tmp`, runs `gh` and `engine`, and
closes. It exits 1 when gh failed or the session was denied, stalled or
skipped a step.

## Seeing and reaching sessions

| Where | How |
| --- | --- |
| Browser | The dashboard (`engine ui`, `control-server.md`), on loopback: on its host or through a tunnel. Its session view adds each attempt's host and observed state. |
| Terminal | `engine ps [--json]` lists every task with its state and attempt. `engine attach <task-id>` runs `tmux attach` on its host, locally or over `ssh -t <address>`. |
| Phone | An SSH app over Tailscale, `tmux attach -t engine`, then `Ctrl-b w`. |
| Apps | Claude: the Claude apps and claude.ai/code, through Remote Control. Codex: the ChatGPT and Codex apps, when the session joined the remote-control daemon. |

## tmux setup

Engine owns the tmux session `engine` on each host. It sets options on that
session and its windows, and never edits `~/.tmux.conf` or global options.
tmux reads `.` as a pane separator, so engine targets windows by id (`@3`),
not name. Windows stay after their command exits (`remain-on-exit`), so a
dead session's last screen and exit stay readable. To type into a session,
engine sends the text with `send-keys -l`, then `Enter` after a short delay:
input boxes can swallow a newline sent with the text.

tmux survives a closed terminal, not a sleeping machine: a macOS host holds
off sleep per window while on power; a Linux host should not suspend.

## Lifecycle

- **Questions:** `engine ask` sets `needs-human`, and the executor waits in
  the live session. Resolution from any surface restores the state and wakes
  it to read its updates.
- **End:** on its merge, an executor reports `engine task cleaned`; the
  coordinator then releases the session (`coordinator.md`).
- **Crash:** the window died but the task is still `running`. The coordinator
  fails it or resumes it (`coordinator.md`), keeping the worktree.
