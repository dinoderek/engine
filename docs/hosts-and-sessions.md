# Hosts and sessions

> **Owns:** host config, task placement, the attempt as the session registry, how sessions are started and reached. **Not here:** dispatch, host ops and session signals (`coordinator.md`), what a session does (the skills, `plugins/engine/skills/`), what it records (`telemetry.md`), each harness's launch line, tiers, permissions and apps (`claude.md`, `codex.md`).

## Hosts

For dispatch, Git credentials and run credential files, load `coordinator.md`.

**One file, every host.** `~/.config/engine/hosts.toml` is the same on every
host: edit it on one, and `engine setup --host <name>` copies it. Each host
keeps its own name in `~/.config/engine/host`.

**Names and addresses.** A host's name is its `[host.<name>]` key, a logical
name such as `mac` or `devbox`, never a hostname or an IP. Its `address`, `<user>@<tailscale name>`, is how other hosts
reach it over plain SSH, passed to `ssh` as is, and is never posted anywhere.
A host is local to the machine whose `~/.config/engine/host` names it and
reached over SSH from every other one.

Every host is a machine and needs `address` and `ram_gb`; engine does not
spawn cloud sessions.

**Slots from memory.** `(ram_gb - reserve_gb) / gb_per_session`, rounded
down, is a machine's concurrent sessions; set the values at the top and
override them per host. `max_sessions` replaces the derived count. Only a live window with a run recorded under its name (an executor's or
the planner's) takes a slot.

**Heavy sessions.** A task is heavy when its card requires one of the
top-level `heavy_capabilities`, such as a simulator or VM. A host's `max_heavy_sessions` caps them below its slots
(a cap above the slots is refused); without it every slot may be heavy.
`status` reports `heavy_slots` and `free_heavy_slots`.

**Capabilities** are declared per host and named by a card's `requires`; the
list is `hosts.Capabilities` in `internal/hosts/hosts.go`. `ios` means iOS
builds and the Simulator.

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

Launch lines and tier models are in `claude.md` and `codex.md`. Each host
pins every harness binary (`bin`), which `{bin}` in the launch line expands
to; engine never uses whichever comes first on `PATH`.

## Placement

In start order, the coordinator gives each ready task a host that meets all
of these, preferring the most free slots (heavy ones, for a heavy task),
then the fewest capabilities, then the lowest name:

- a free slot from the host's `status` (none if its `status` fails), and a
  free heavy slot for a heavy task;
- the chosen harness, under its cap across hosts, and signed in there;
- every capability in the card's `requires`.

For each unpinned card, `harness_ratio` scores positive-weight harnesses by
`(live sessions across hosts + 1) / weight`, counting pinned cards and every
live session. Lowest wins; ties prefer `default_harness`, then name. If no
host fits, try the next harness. Zero weights exclude unpinned starts. No
ratio: `default_harness`. Unknown harnesses and all-zero ratios are refused.

An unplaced task stays ready and waits; the operator is notified once if no
host could take it even with all slots free. The planner starts at `deep` on
the plan's harness, else `default_harness`, once signed in there.

## Attempts and naming

An attempt is the session registry: the server's record of a task's or plan's
session, with its host, harness, run ID, tmux window, state and timestamps
(`engine attempt list [<task-id>]`). `engine ps`, `engine attach` and the
dashboard resolve a session through its current attempt.

A session is named `<repo>-<task>`, such as `boga3-412`, on every surface: the
tmux window, the remote-control name and part of the branch. It holds letters,
digits, `_`, `.` and `-`, and never starts with `-`.

## Starting a session

The coordinator dispatches a session on the host placement picks
(`coordinator.md`). It decides the model, effort and kickoff from the task's
tier, then has the host run `control-spawn`: it syncs engine's clone, then runs
the steps below. Spawn refuses when the host file, `hosts.toml` or this host's entry is
missing, the harness is unknown, not run on the host or not executable, or
the window exists. Slots, capabilities and the harness cap are placement's
alone. A host on another protocol fails, naming `engine setup --host <name>`
(`operator-setup.md`).

The host:

1. syncs engine's clone using normal Git configuration, clearing
   engine-generated credential and identity overrides; clones over HTTPS on
   first use, fetches, and leaves it at the remote's default branch;
2. creates the worktree on `task/<repo>-<task>`, with the repo's own
   `worktree create` when it provides one (`project-context.md`,
   "Worktrees"), else `git worktree add` from the default branch, refusing
   a branch on `origin`, or a local one with commits (one without is
   remade). A resume opens on `origin`'s branch at its tip, refusing a local
   branch with commits `origin` lacks; a clean worktree left there with no
   such commits is adopted and fast-forwarded, else refused;
3. trusts the worktree for its harness (`claude.md` and `codex.md`,
   "Starting");
4. opens the window `<repo>-<task>` in the session `engine`, in the worktree;
5. runs the host's launch line at the task's tier (default `standard`) with
   the flags the harness adds at start, then `--` and the kickoff: the
   session's first message, naming the `engine` skill's server executor or
   planner mode, the task or plan, the attempt and the reviewer model.
   `/bin/sh` exports `ENGINE_RUN_DIR`, `ENGINE_HARNESS`, the server's URL,
   credential file and attempt, and `ENGINE_UNATTENDED=1` when hands-off. A Codex session joined
   to remote control gets the `ENGINE_*` variables on its command line instead
   (`codex.md`, "Starting");
6. writes `run.json` (`telemetry.md`; a Codex session joined to remote
   control gets it before step 4, since its hooks find their run by it).

**Preflight:** `engine start --check [--harness <name>] [--host <name>]`
(Claude without `--harness`) runs `gh auth status` in the `engine` tmux server
(`operator-setup.md`, "GitHub access"), then a hands-off light-tier session,
`engine-check`, reads the skill, writes and reads a file in `/tmp`, runs `gh`
and `engine`, and closes. It exits 1, printing what it met, when gh failed or
the session was denied, stalled or skipped a step.

## Seeing and reaching sessions

| Where | How |
| --- | --- |
| Browser | The dashboard (`engine ui`, `control-server.md`), on loopback: on its host or through a tunnel. Its session view adds each attempt's host and observed state. |
| Terminal | `engine ps [--json]` lists every task with its state and attempt. `engine attach <task-id>` runs `tmux attach` on its host, locally or over `ssh -t <address>`. |
| Phone | An SSH app over Tailscale, `tmux attach -t engine`, then `Ctrl-b w`. |
| Apps | Claude: the Claude apps and claude.ai/code, through Remote Control. Codex: the ChatGPT and Codex apps, when the session joined engine's remote-control daemon. For setup, load `claude.md` or `codex.md`, "Apps". |

## tmux setup

Engine owns the tmux session `engine` on each host. It sets options on that
session and its windows, and never edits `~/.tmux.conf` or global options. tmux reads `.` as a pane separator,
so engine targets windows by id (`@3`), not name. Windows stay after their
command exits (`remain-on-exit`), so a dead session's last screen and exit
stay readable. To type into a session, engine sends the text with `send-keys
-l`, then `Enter` after a short delay: input boxes can swallow a newline sent
with the text.

tmux survives a closed terminal, not a sleeping machine: a macOS host holds
off sleep per window while on power; a Linux host should not suspend.

## Lifecycle

- **Questions:** the executor asks with `engine ask`, which sets `needs-human`,
  and waits in the live session. Answer from any surface, the operator inbox
  included: the executor reads its updates when it resumes.
- **End:** after cleanup an executor closes its window.
- **Crash:** the window died but the task is still `running`. The coordinator
  marks it `failed` or resumes it (`coordinator.md`), keeping the worktree.
