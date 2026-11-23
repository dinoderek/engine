> **Owns:** server deployment, records, API, credentials, the inbox and host status. **Not here:** dispatch, recovery, the merge queue, host calls (`coordinator.md`), the dashboard and the session view (`dashboard.md`), session signals (`session-signals.md`), host configuration (`hosts-and-sessions.md`), executor practices (the `engine` skill).

# Control server

`engine serve` owns plans, tasks, updates, attempts and merge queue entries, and runs the coordinator. GitHub supplies code, PRs and CI. For dispatch, recovery and merges, load `coordinator.md`.

## Start

The server needs `tmux`. Each operator's `~/.config/engine/config.toml` names its host from `hosts.toml` (`[server] host = "devbox"`):

```sh
mkdir -p ~/.config/engine
(umask 077; openssl rand -hex 32 > ~/.config/engine/server-token)
engine serve
```

`engine serve` starts the server on that host (over ssh if remote) and returns. It runs in tmux server `engine-server` (`tmux -L engine-server attach`), logs to `~/.local/share/engine/server/serve.log`, and listens on port 7331 of the host's Tailscale name, which it advertises. To restart, close its window and rerun.

A host whose `hosts.toml` entry records `services` starts through its systemd unit, never a second tmux server; a failed start exits 1, naming its `journalctl --user -u`.

`--foreground` runs it in place: for other networks, use it behind an SSH tunnel or TLS proxy, setting `[server] url` instead of `host`. Without config.toml, the CLI uses `http://devbox:7331`; `ENGINE_SERVER_URL` overrides both. The CLI reads its credential from `~/.config/engine/server-token` or `ENGINE_SERVER_TOKEN_FILE`; workers get the advertised URL.

For backup, stop the server, run `engine serve --backup <new-path>`, restart it, and copy the backup off-machine. Restore with the server stopped, keeping the operator credential. Backups hold credentials: keep them private.

## Records and commands

IDs are global across repositories, plans, tasks and attempts. Every task belongs to one plan; creating a task without `--plan` fails. `engine help` lists commands; flags are in `internal/controlcmd/command.go`.

Edits, approvals and workflow changes require the current `--version`. A plan's edit invalidates its approval; an active execution, a plan's children included, prevents an edit. Approve, run and stop take a plan and persist its coordination intent (`coordinator.md`). `get`, `list` and `plan-view` add a plan's derived, unstored `status` (`execution-protocol.md`).

`engine lint <plan-id>` checks a plan's tasks before approval, changing nothing, from one snapshot including outside dependencies.

**Stopping a task.** Operator-only `engine task stop <task>` ends a running task's execution without failing it: in one transaction it holds the task, ends its attempt `stopped` (leaving the merge queue) and sets the task `todo`. Placement and `claim` skip a held task. `engine task resume <task>` lifts the hold; the next dispatch resumes the kept branch (`hosts-and-sessions.md`). Stopping without a running execution, or resuming a task not held, is a 409.

**Moving a task.** Operator-only `engine task move <task> [--host <h>] [--harness <h>]` ends a running task's attempt `moved`, keeping its state and queue place, and sends its resume to that host (else off the old one) and harness. Off a host the latest pass did not read, it is a 409 (`move-unconfirmed`: the old session may still push) until `--yes` or a terminal confirms. For its dispatch, load `coordinator.md`.

**Pausing a host.** Operator-only `engine host pause|resume <host>` sets or lifts a pause in the host's record: placement skips it, planners too, and a dispatch to it is a 409; sessions carry on; `engine host move <host> [--harness <h>] [--yes]` pauses it and moves each task running there, or only that harness's, as `task move` does. A task still dispatching is left for a repeat. An unread host is one `move-unconfirmed` 409 for all, until `--yes` or a terminal confirms.

**Completing outside engine.** `engine task complete-external <task-id> --version <n> --pr <url>` records outside work. For an operator, on an inactive todo or failed task with no open question, it records done and a PR URL, unverified.

## API and storage

`POST /v1/command` accepts the Request in `internal/control/types.go`. Unknown fields, missing credentials and unknown operations fail; the credential sets authorship. Errors carry an HTTP status and sometimes a stable `reason`; storage details stay in server logs. A refused `engine attempt reconcile` is a 409 with reason `reconcile-refused`; the CLI never retries it.

Every write has a request key; reusing one for another command fails. A call retries uncertain delivery once with the same key. A session's command (one with `ENGINE_ATTEMPT_ID`) also retries connect errors, timeouts and 5xx for 90 seconds, under that key (`internal/controlcmd/retry.go`), and its first retry prints one stderr line naming the key; a 4xx never retries. `ENGINE_RETRY_FOR` (a Go duration, `0` for none) sets the window for every server command but `engine attach`; the operator's default is none; the prompt hook and the dashboard keep their own behaviour. Version conflicts require reading the current record. An edit invalidates a placement decision before any execution is reserved or launched.

The server alone writes its database, in process (`modernc.org/sqlite`), a table per record kind. Reads are WAL transactions beside one writer. An older schema is first copied to `<db>.pre-v7`, owner-only (an existing copy stops it), then migrated in one transaction; newer is refused.

The operator credential is an owner-only regular file. Every dispatch creates a separate execution credential, scoped to that plan or task; workers cannot approve, start coordination or read the operator inbox. Ending an attempt revokes its credential. Credentials are hashed for lookup; dispatch receipts retain the token privately for interrupted dispatch recovery.

## Updates and operator inbox

Updates are immutable and ordered; read up to 200 after a cursor. A question atomically holds a task in `needs-human`: its own, or the asking task (the update's `task`) when a task's session asks on its plan. Acknowledgements and replies leave it unresolved. Resolution records the decision and atomically restores each held task's earlier state, unless another question holds it, its state changed or its execution ended. A task's own questions, including one it asked on its plan, block its state changes, completion, merge and quota resume. A plan's question blocks only planner handoff and the first dispatch of its tasks. A **fleet question**, the coordinator's about a host (`host`), has no item and holds nothing; for what an answer runs, load `coordinator.md` (unreachable) or `session-signals.md` (limit).

**Choices and class.** `ask` takes `choices`, `recommended` (an index) and `class` (a hard-stop class in lower case); `internal/control/question.go` holds the limits. The update keeps them: `updates`, `inbox`, a plan's `question` and `feedback` return them. A resolution by choice records its text as the body, then any note. Resolving a resolved question, by the operator or a pane answer, is a 409 with reason `already-resolved`, writing nothing; a replayed request key returns its first result.

**Pane input.** `pane-input`, which only a session's prompt hook posts (`harnesses.md`), records the operator's pane text verbatim for the session's own item. It records a `pane-answer` resolving the oldest open question holding it that was asked before the text was typed (host clock, ±30 seconds), the session's own first, never a task's for a planner, and releases holds as a resolution does; else, or for a harness command such as `/compact`, it is a `pane-message` resolving nothing. A post unsigned, failing the attempt's `pane_key`, or missing from the transcript still counts, adding one coordinator note to the operator.

Messages addressed to the operator appear in the cross-repository inbox. Informational ones leave it on acknowledgement, singly or in bulk (`ack-all`, never questions); questions leave on resolution. Idle, unrestricted sessions get wake-up pointers for others' messages on their item; tasks also for others' plan messages and resolutions, except coordinator notices and those to the operator.

## Host status

`host-status`, an operator-only read, returns the fleet as the latest coordinator pass read it: per `hosts.toml` host, reachability, slots, windows with attempts, quota and readiness. It is held in memory, starts empty and never contacts a host, so it ages while no plan is coordinating. `engine host status --all [--json]` prints it. Unlike it, each host's last tmux start time is a stored record (`session-signals.md`, "Host-wide loss").
