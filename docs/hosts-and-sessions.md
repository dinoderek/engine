# Hosts and sessions

> **Owns:** host config, task placement, the attempt as the session registry, how sessions are started and reached. **Not here:** dispatch and host ops (`coordinator.md`), session signals (`session-signals.md`), what a session does (the skills, `plugins/engine/skills/`), what it records (`telemetry.md`), each harness's launch line, tiers and adapter (`harnesses.md`).

## Hosts

For dispatch, Git credentials and run credential files, load `coordinator.md`.

**One file, every host.** `~/.config/engine/hosts.toml` is the same on every
host (`engine setup --host <name>` copies it); each host's own name is in
`~/.config/engine/host`.

**Names and addresses.** A host's name is its `[host.<name>]` key, never a
hostname or IP. Its `address` goes to `ssh` as is and is never posted.

**Slots.** `(ram_gb - reserve_gb) / gb_per_session`, rounded down, is a
host's concurrent sessions; `max_sessions` replaces it. Only a live window
with a run recorded under its name takes a slot.

**Heavy sessions.** A task is heavy when its card requires one of the
top-level `heavy_capabilities`. A host's `max_heavy_sessions` caps them below
its slots.

**Capabilities** are declared per host and named by a card's `requires`. The
one list is `hosts.Capabilities` in `internal/hosts/hosts.go`.

```toml
default_harness = "claude"
harness_ratio = { claude = 4, codex = 1 }
gb_per_session = 8
reserve_gb = 8
heavy_capabilities = ["ios", "vm"]

[host.mac]
address = "<user>@<tailscale name>"
harnesses = ["claude", "codex"]
ram_gb = 48
reserve_gb = 16         # also the operator's workstation
max_heavy_sessions = 2  # at most 2 of its 4 sessions heavy
capabilities = ["macos", "ios", "vm", "docker"]
bin = { claude = "~/.local/bin/claude", codex = "~/.local/bin/codex" }

[host.devbox]
address = "<user>@<tailscale name>"
harnesses = ["claude", "codex"]
ram_gb = 32
capabilities = ["linux", "vm", "docker"]
bin = { claude = "~/.local/bin/claude", codex = "~/.local/bin/codex" }

[host.devbox.services]
manager = "systemd"
server = "engine-server.service"
```

**Services.** The optional `services` table says how a host runs the control
server, a `systemctl --user` unit; `config.toml`'s `[server] host` says which
host runs it. Another manager, one key without the other, and a unit that is
not a `.service` name are refused at load.

Each harness runs through its pinned `bin`, never whichever binary is first
on `PATH`. Launch lines and tiers are in `harnesses.md`.

## Placement

In start order, each ready task goes to a host that has a free slot (a free
heavy slot, for a heavy task), the chosen harness signed in and under its cap,
every capability the card requires, a tmux server whose working directory
exists (below), and a passed readiness probe for its repo (`readiness.md`).
Among those, the host with the most free slots wins, then the fewest
capabilities, then the lowest name.

For an unpinned card, `harness_ratio` scores each positive-weight harness by
`(live sessions across hosts + 1) / weight`; the lowest wins, ties prefer
`default_harness`, then name. If no host fits, the next harness is tried.
Without a ratio, `default_harness` is used.

An unplaced task stays ready; the operator is notified once if no host could
take it even with every slot free. The planner starts at `deep` on the plan's
harness, else `default_harness`.

## Attempts and naming

An attempt is the session registry: the server's record of a task's or plan's
session (host, harness, run ID, tmux window, state; `engine attempt list`).
`engine ps`, `engine attach` and the dashboard resolve a session through it.

A session is named `<repo>-<task>` in its tmux window, run ID and branch; the
harness apps title it `t<n> · <title>` (`p<n>` for a planner).

## Starting a session

The coordinator picks the host (`coordinator.md`); its `control-spawn` then:

1. creates the worktree on `task/<repo>-<task>`, with the repo's
   `worktree create` when it has one, else `git worktree add`. A resume
   reopens `origin`'s branch at its tip;
2. trusts the worktree for its harness and opens the window `<repo>-<task>`
   in the session `engine`;
3. runs the launch line at the task's tier with the adapter's flags and the
   kickoff, which names the `engine` skill's executor or planner mode, the
   task or plan, the attempt and the reviewer model. `ENGINE_*` variables and
   `ENGINE_UNATTENDED=1` (hands-off) come from `/bin/sh`, or from the adapter
   when the session joins a remote-control daemon;
4. writes `run.json` (`telemetry.md`), before the window opens for a
   daemon-joined session, since its hooks find their run by it.

Spawn refuses a missing host file, `hosts.toml` or host entry, an unknown or
unrunnable harness, or an existing window; slots, capabilities and caps are
placement's alone. A host on another protocol names `engine setup --host`.

**Preflight:** `engine start --check` runs `gh auth status`, then a hands-off
light-tier session that exercises the skill, `gh`, `engine` and `/tmp`; it
exits 1 when gh failed or the session was denied, stalled or skipped a step.

## Seeing and reaching sessions

| Where | How |
| --- | --- |
| Browser | The dashboard, `engine ui`, on loopback or through a tunnel. |
| Terminal | `engine ps` lists tasks with their attempts; `engine attach <task-id>` runs `tmux attach` on the host, over `ssh -t` if remote. |
| Phone | An SSH app over Tailscale: `tmux attach -t engine`, `Ctrl-b w`. |
| Apps | Claude apps and claude.ai/code through Remote Control; Codex apps when the session joined the daemon. |

## tmux setup

Engine owns the tmux session `engine` and sets options only on it and its
windows, never globally or in `~/.tmux.conf`. It targets windows by id
(`@3`): tmux reads `.` as a separator. Windows stay after their command
exits (`remain-on-exit`). Engine types with `send-keys -l`, then `Enter` after
a short delay: input boxes can swallow a newline sent with the text.

**Working directory.** Every tmux command runs from `$HOME`, so no server
engine starts lives in a worktree later removed, failing every later
window. Host status reports one as `tmux_cwd_gone`, with its restart
command; placement skips the host. Engine never kills a server with sessions.

**Creation.** A per-server lock, `tmux-<socket>.lock` in
`~/.local/share/engine`, serialises window creation. A failed `respawn-pane`
is retried once; its error carries `list-panes -a`.

tmux survives a closed terminal, not a sleeping machine: macOS holds off
sleep per window on power; a Linux host should not suspend.

## Lifecycle

- **Questions:** `engine ask` sets `needs-human`; resolution from any surface
  wakes the session to read its updates.
- **Crash:** a dead window leaves the task `running`; the coordinator fails
  it or resumes it, keeping the worktree.
