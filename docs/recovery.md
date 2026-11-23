# Recovery

> **Owns:** what engine does when each of its eight failures hits, how each is detected, and the commands an operator or an agent runs to recover, including a planned reboot. **Not here:** the rules behind each response (`session-signals.md`, `coordinator.md`), host config and the server unit (`hosts-and-sessions.md`, `operator-setup.md`), the watcher's messages (`telegram.md`), the command reference (`control-server.md`).

Engine has three layers. The server (`engine serve`) runs the coordinator. Each host runs one tmux server with one session, `engine`. Each task gets one window in it, running its agent session. A lost window and a lost session look the same to engine; only what killed it differs. A failure ends the work of the layer it hits: one window ends one task, a host's tmux server or reboot ends every task on that host, and a server failure ends nothing but cuts sessions off from their records.

Engine never asks before it resumes a dead task. It asks only when a choice belongs to you: a host unreachable for 15 minutes, or a session at its usage limit. A server down is reported by message, and a hung session by a notice; neither is a question.

## The failures

### 1. The server crashes

- **Detected:** on a host whose `hosts.toml` records a `services` unit, systemd restarts it (`Restart=on-failure`, `operator-setup.md`). Elsewhere nothing restarts the server. Sessions notice because their `engine` commands fail.
- **Engine does:** a session's command retries connect errors, timeouts and 5xx for 90 seconds under its request key (`control-server.md`). An outage long enough to count wakes each idle session of a running task once and restarts stall timers (`session-signals.md`, "Server outage"). A shorter one gets only the retry. Sessions on the server's host run in their own tmux scope, so the unit's restart does not end them, unless host status reports `tmux_unit` (`tmux.md`, `operator-setup.md`).
- **Run:** nothing, if it comes back. If it does not, go to 8.

### 2. The server is upgraded or restarted

- **Detected:** it is planned.
- **Engine does:** `engine upgrade` stops the unit, backs up the database, installs the new build and plugins, sets up the server's host, starts the server, then sets up each other host (`operator-setup.md`, "Upgrading"). Busy tasks carry on, and a session that dies during the upgrade is an engine bug: it keeps its tail and sends a notice (`session-signals.md`, "Upgrade"). A host not yet set up answers on the old protocol and is refused, so its sessions show unknown, not dead, and nothing new is placed there. An old plugin version stays while a window still runs it (`harnesses.md`).
- **Run:**
  1. `engine upgrade --dry-run`, to read the steps.
  2. `engine upgrade`, from any host in `hosts.toml`.
  3. For a host it reports, `engine setup --host <name>`, then rerun the upgrade.

  Rolling back the database is manual. The backup and the rules for an older database are in `control-server.md`.

### 3. One window or session is lost

- **Detected:** the next successful host read shows the window gone or its command exited.
- **Engine does:** it applies the vanish rules (`session-signals.md`, "Vanish"). A claimed task keeps its PR and queue place and resumes on its own branch. Three such deaths in a row (`control.DeathCap`) end the task `failed`, with a notice carrying the last two pane-log tails. An unclaimed death is an early death: the first returns the task to `todo`, the second fails it. A death at a limit, or after coordination stopped, does not count.
- **Run:**
  1. `engine updates <task>` and `engine attach <task>` to read why it died.
  2. Nothing, for a resumed task. For a `failed` task, fix the cause, then set it back to `todo` with `engine task state <task> --version <n> --state todo`. The dashboard's retry does the same (`dashboard-writes.md`).

### 4. The tmux server dies, or the host reboots

- **Detected:** the first successful read after the host is back. Its tmux start time differs from the one saved, and every window last seen live is gone; or no tmux server runs at all (`session-signals.md`, "Host-wide loss"). Until then, the host is unreachable (7).
- **Engine does:** it reads this as one host-wide loss, not one death per task, and sends one notice for the host. The attempts it ends resume or return to `todo` without counting toward a cap or strike. A stale `.git/index.lock` in a kept worktree is removed before the resume (`hosts-and-sessions.md`). A `tmux kill-session -t engine` that leaves the server running counts each task's death instead.
- **Run:** nothing, if the host comes back. For a planned reboot, see "Planned reboot" below.

### 5. A session reaches its usage limit

- **Detected:** the harness's limit marker and state: `limited`, or waiting with a reset time.
- **Engine does:** a live limited session gets `continue` after the reset plus 2 minutes, or every 30 minutes when the reset is unknown (`session-signals.md`, "Limit recovery"). When another host or harness has room, the operator gets one question with the wait and move choices (`session-signals.md`, "Limit choice"). A session that died at its limit ends `vanished`, uncounted. Its host and harness take nothing new until the reset, or one task per 30 minutes if the reset is unknown; another host or harness may take the task sooner (`session-signals.md`, "Death at a limit").
- **Run:** answer the question in `engine inbox --operator`, or run the move yourself:
  - `engine task move <task> --harness <h> --host <h>`
  - `engine host move <host> --harness <h>` for several tasks on one harness

  Add `--yes` only when you have checked that the old host is not still running the task (see 7).

### 6. A session hangs while "working"

- **Detected:** a `working` session shows no transcript or pane-log activity for 60 minutes. It gets one notice per quiet stretch, with the task, host, quiet time and last 20 screen lines (`session-signals.md`, "Hung session").
- **Engine does:** nothing else. It stops nothing.
- **Run:**
  - `engine attach <task>` to look.
  - `engine tell <task> <text>` to nudge it.
  - `engine task stop <task>` to end it, holding the task in `todo` with its worktree and branch kept, or `engine task move <task>` to restart it elsewhere.

### 7. A host is unreachable for long

- **Detected:** its `status` call fails (ssh exit 255 is an unreachable host). The host shows as unreachable on the dashboard.
- **Engine does:** its sessions are unknown, never dead: nothing is ended or moved, since they may still run. New tasks go to other hosts, and a merge-queue head there ends `merge-refused` after a bounded wait (`coordinator.md`, "Merge queue"). After 15 minutes with running tasks there, the operator gets one question, with three choices (`coordinator.md`, "An unreachable host is asked about"): wait, which is the default; move its tasks; or stop each. If the host returns first, the question resolves itself, and one note lists what happened to each task.
- **Run:**
  - `engine host pause <host>`, to stop new placement there while you decide.
  - `engine host move <host>`, to move every task off it.
  - `engine task stop <task>`, for each task you want stopped.

  Moving off an unread host asks for confirmation, since the old session may still push. The old session's credential is revoked, so it cannot write to the server or request a merge (`control-server.md`, "API and storage"). Each kickoff warns that it may still push, and before any push the new session asks you if `origin`'s branch holds commits it lacks (`coordinator.md`, "Dispatch and recovery"). When the old host returns, its window is released, keeping the worktree, and you are told what was left unpushed.

### 8. The server is down and not coming back

- **Detected:** `engine watch`, a launchd agent on the operator's Mac, calls the server every minute. Two failures in a row send one Telegram message with the recovery commands (`telegram.md`, "The watcher"). The server cannot report its own death, so the watcher is the only signal.
- **Engine does:** nothing until the server returns. Sessions retry (1) and are woken once the gap ends. A second message says the server answers again.
- **Run:**
  - `engine server status`: the unit, API, database, disk, and the last 50 log lines. It reaches the server through the host that `hosts.toml`'s `services` table names.
  - `engine server restart`, to restart its unit.
  - If the server's host was off, the enabled unit starts the server at boot once the host is up (`operator-setup.md`, "The server's unit").

## Planned reboot

- **A session host:** run `engine host move <host>` first. It pauses the host and moves each of its tasks elsewhere. Once its windows are released and their branches pushed, reboot. Then run `engine host resume <host>`. Without the move, the reboot is still safe: the host-wide loss resumes its tasks without counting them (4).
- **The server's host:** rebooting it also stops the server, so the server's outage rules (1) and the watcher (8) apply. Run `engine server status` once it is back.

Every command's flags and behaviour are in `control-server.md`, and every write takes a request key, so a repeat after a lost reply is safe.
