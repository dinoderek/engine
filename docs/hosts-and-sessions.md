# Hosts and sessions

> **Owns:** host config, task placement, the attempt as the session registry, how sessions are started and reached. **Not here:** dispatch and host ops (`coordinator.md`), session signals (`session-signals.md`), what a session does (the skills, `plugins/engine/skills/`), what it records (`telemetry.md`), each harness's adapter (`harnesses.md`) and launch line and tiers (`launch-lines.md`), how engine drives tmux (`tmux.md`).

## Hosts

For dispatch, Git credentials and run credential files, load `coordinator.md`.

**One file, every host.** `~/.config/engine/hosts.toml` is the same on every
host (`engine setup --host <name>` copies it); each host's own name is in
`~/.config/engine/host`.

**Names and addresses.** A host's name is its `[host.<name>]` key, never a
hostname or IP. Its `address` goes to `ssh` as is, never posted.

**Slots.** `(ram_gb - reserve_gb) / gb_per_session`, rounded down, is a
host's concurrent sessions; `max_sessions` replaces it. Only a live window
with a run under its name takes a slot.

**Heavy sessions.** A task is heavy when its card requires a top-level
`heavy_capabilities` entry; a host's `max_heavy_sessions` caps heavy sessions
below its slots.

**Capabilities** are declared per host and named by a card's `requires`
(`hosts.Capabilities`, `internal/hosts/hosts.go`).

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
reserve_gb = 16
max_heavy_sessions = 2
capabilities = ["macos", "ios", "vm", "docker"]
bin = { claude = "~/.local/bin/claude", codex = "~/.local/bin/codex" }

[host.devbox]
address = "<user>@<tailscale name>"
harnesses = ["claude", "codex"]
ram_gb = 32
capabilities = ["linux", "vm", "docker"]
bin = { claude = "~/.local/bin/claude", codex = "~/.local/bin/codex" }
```

**Services.** The optional `services` table names the control server's
`systemctl --user` unit on its host. Another manager, one key without the other, or a unit that is not
a `.service` name is refused at load.

Each harness runs through its pinned `bin`, never a `PATH` lookup. For launch lines and tiers, load `launch-lines.md`.

## Placement

In start order, each ready task goes to a host that has a free slot (a free
heavy slot, for a heavy task), the chosen harness signed in and under its cap,
every capability the card requires, a tmux server whose working directory
exists (`tmux.md`), a passed readiness probe for its repo (`readiness.md`),
and no usage limit holding that host × harness (`session-signals.md`, "Death
at a limit").
Among those, the host with most free slots wins, then the fewest
capabilities, then the lowest name.

For an unpinned card, `harness_ratio` scores each positive-weight harness by
`(live sessions across hosts + 1) / weight`; the lowest wins, ties prefer
`default_harness`, then name. If no host fits, the next harness is tried.
Without a ratio, `default_harness` is used.

**Quota.** An unpinned card tries near-full harnesses last, unless all are:
those that used `quota_threshold` percent of any window, by default
`{ five_hour = 90, seven_day = 95 }`. The fleet's newest reading counts;
a window past its reset, or absent, is empty.

An unplaced task stays ready; the operator is notified once if no host could
take it with every slot free. The planner starts at `deep` on the plan's
harness, else `default_harness`.

## Attempts and naming

An attempt is the session registry: the server's record of a task's or plan's
session (host, harness, run ID, tmux window, state; `engine attempt list`).
`engine ps`, `engine attach` and the dashboard resolve through it.

A session is named `<repo>-<task>` in its tmux window, run ID and branch; the
harness apps title it `t<n> · <title>` (`p<n>` for a planner).

## Starting a session

The coordinator picks the host (`coordinator.md`); its `control-spawn` then:

1. creates the worktree on `task/<repo>-<task>`, with the repo's
   `worktree create` when it has one, else `git worktree add`, and marks the
   branch with its run (`branch.<branch>.engineRun`). A re-dispatch adopts a
   branch a run of the same task marked, whether or not that run opened a
   window: as it is, with its unpushed commits and uncommitted changes,
   fast-forwarded to `origin`'s tip when clean and behind it, and refused when
   the two diverged or `origin` is ahead of uncommitted changes; the kickoff
   names its head. A resume of an unmarked branch reopens `origin`'s at its tip;
2. trusts the worktree, and any directory the launch adds, for its harness, and opens the window `<repo>-<task>`
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
placement's alone.

## Moving a task

A moved task (`control-server.md`) resumes on its old host from the kept
worktree, uncommitted work included, and elsewhere from `origin`'s branch.
It waits while a host read shows its old window, which each pass releases
with `release --moved` (`coordinator.md`), or while the old host is unread,
unless the operator moved it blind. A blind move's kickoff warns that the
old session may still push: before each push, the new session fetches and
asks the operator when `origin`'s branch has commits it lacks. A release
leaving work `origin` lacks tells the operator, unless the move stayed on
that host.

**Preflight:** `engine start --check` runs `gh auth status`, then a hands-off light-tier session exercising the skill, `gh`, `engine` and `/tmp`; it exits 1 if gh failed or the session was denied, stalled or skipped a step.

## Seeing and reaching sessions

| Where | How |
| --- | --- |
| Browser | The dashboard, `engine ui`, on loopback or through a tunnel. |
| Terminal | `engine ps` lists tasks with their attempts; `engine attach <task-id>` runs `tmux attach` on the host, over `ssh -t` if remote; `engine host status --all` shows the fleet. |
| Phone | An SSH app over Tailscale: `tmux attach -t engine`, `Ctrl-b w`. |
| Apps | Claude apps and claude.ai/code through Remote Control; Codex apps when the session joined the daemon. |
