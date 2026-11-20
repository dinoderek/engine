> **Owns:** server deployment, records, API, credentials, the inbox and host status. **Not here:** dispatch, recovery, the merge queue, host calls (`coordinator.md`), the dashboard and the session view (`dashboard.md`), session signals (`session-signals.md`), host configuration (`hosts-and-sessions.md`), executor practices (the `engine` skill).

# Control server

`engine serve` owns plans, tasks, updates, attempts and merge queue entries, and runs the coordinator. GitHub supplies code, PRs and CI. For dispatch, recovery and merges, load `coordinator.md`.

## Start

The server needs `sqlite3` and `tmux`. Each operator's `~/.config/engine/config.toml` names its host from `hosts.toml` (`[server] host = "devbox"`):

```sh
mkdir -p ~/.config/engine
(umask 077; openssl rand -hex 32 > ~/.config/engine/server-token)
engine serve
```

`engine serve` starts the server on that host (over ssh if remote) and returns. It runs in tmux server `engine-server` (`tmux -L engine-server attach`), logs to `~/.local/share/engine/server/serve.log`, and listens on port 7331 of the host's Tailscale name, the origin it advertises and the CLI uses. The tailnet keeps credentials confidential. To restart, close its window and rerun.

A host whose `hosts.toml` entry records `services` starts through its systemd unit (`systemctl --user start`), never a second tmux server. A start that fails or leaves the unit inactive exits 1, naming its `journalctl --user -u`.

`--foreground` runs it in place. For other networks, use an SSH tunnel or a TLS reverse proxy, set `[server] url` instead of `host`, and use `--foreground`. Without config.toml, the CLI uses `http://devbox:7331`; `ENGINE_SERVER_URL` overrides both. The CLI reads its credential from `~/.config/engine/server-token` or `ENGINE_SERVER_TOKEN_FILE`; workers get the advertised URL.

For backup, stop the server, run `engine serve --backup <new-path>`, restart it, and copy the backup off-machine. Restore with the server stopped, keeping the operator credential. Backups hold credentials, are private and never overwrite a file. Test restores in an isolated database first.

## Records and commands

IDs belong to engine: repositories, plans, tasks and attempts have separate prefixes and globally unique numbers. A plan is a feature-level requirements/design document. Every task belongs to one plan; creating a task without `--plan` fails. `engine help` lists the command groups; the flags are in `internal/controlcmd/command.go`.

Edits, approvals and workflow changes require the current `--version`. A plan's edit invalidates its approval; an active execution, a plan's children included, prevents an edit. Approve, run and stop take a plan and persist its coordination intent (`coordinator.md`). Retrying a failed task is an explicit operator state change. `get`, `list` and `plan-view` add a plan's derived, unstored `status` (`execution-protocol.md`, "States").

`engine lint <plan-id>` checks a plan's tasks before approval, changing nothing, from one snapshot including outside dependencies.

**Stopping a task.** Operator-only `engine task stop <task>` ends a running task's execution without failing it: in one transaction it holds the task, ends its attempt `stopped` (revoking its credential and leaving the merge queue) and sets the task `todo`. Placement and `claim` skip a held task, and no pass can dispatch it in between. The coordinator then closes the window, keeping the worktree and branch (`coordinator.md`). The card can be edited while held. `engine task resume <task>` lifts the hold; the next dispatch resumes the kept branch, adopted as it is, uncommitted work included, unless it and `origin`'s diverged (`hosts-and-sessions.md`). Stopping a task with no running execution or a dispatch in flight, or resuming one not held, is a 409.

`engine task complete-external <task-id> --version <n> --pr <url>` records work finished outside engine on a plan's task. It is operator-only, for an inactive task in todo or failed with no unresolved question. It takes a PR URL in the task's repository, does not verify the merge, records done and the PR in versioned history, keeps dependencies and prior attempts, and needs no attempt. Running and in-review still require an execution for ordinary transitions.

## API and storage

`POST /v1/command` accepts the Request in `internal/control/types.go`. Unknown fields, missing credentials and unknown operations fail; the credential sets authorship. Errors include an HTTP status, and some a stable `reason`; storage details stay in server logs. A refused `engine attempt reconcile` is a 409 with reason `reconcile-refused`, which the CLI prints and never retries.

Every write has a request key; a key reused for a different command fails. The CLI retries uncertain delivery once with the same key. Version conflicts require reading the current record. An edit invalidates a placement decision before any execution is reserved or launched.

SQLite runs locally through its CLI, with SQL on stdin; one server owns a database. Each transaction updates current records, history and its request receipt together. An older database migrates on open; an unknown schema version is rejected.

The operator credential is an owner-only regular file. Every dispatch creates a separate execution credential, scoped to that plan or task; workers cannot approve, start coordination or read the operator inbox. Ending an attempt revokes its credential. Credentials are hashed for lookup; dispatch receipts retain the token privately for interrupted dispatch recovery.

## Updates and operator inbox

Updates are immutable and ordered; read up to 200 after a cursor. Comments leave workflow state alone. A question atomically holds a task in `needs-human`: its own, or the asking task (the update's `task`) when a task's session asks on its plan. A task's session asks only on its task or plan. Acknowledgements and replies leave it unresolved. Resolution records the decision and atomically restores each held task's earlier state, unless another question holds it, its state changed or its execution ended. A task's own questions, including one it asked on its plan, block its state changes, completion, merge and quota resume. A plan's question blocks only planner handoff and the first dispatch of its tasks.

**Pane input.** `pane-input`, which only a session's prompt hook posts (`harnesses.md`), records the operator's pane text verbatim for the session's own item: a `pane-answer` resolving the oldest open question holding it that was asked before the text was typed (by the host's clock, give or take 30 seconds), the session's own first, released as a resolution is; else a `pane-message` resolving nothing. A post unsigned, failing the attempt's `pane_key`, or missing from the transcript still counts, and adds one coordinator note to the operator in its transaction.

Messages addressed to the operator appear in the cross-repository inbox. Informational messages leave the inbox on acknowledgement; questions leave on resolution. Idle, unrestricted sessions get wake-up pointers for others' messages on their item; tasks also for others' plan messages and resolutions, except coordinator notices and those to the operator. Messages persist when wake-up fails.

## Host status

`host-status`, an operator-only read, returns the fleet as the latest coordinator pass read it: when, and for each `hosts.toml` host its reachability, slots, windows with their attempts, quota and readiness. It is kept in memory, starts empty with the server and never contacts a host. Passes read hosts only while a plan is coordinating, so the status ages when none is. `engine host status --all [--json]` prints it.
