> **Owns:** server deployment, records, API, credentials, the inbox, the dashboard and the session view. **Not here:** dispatch, recovery, the merge queue, host calls and session signals (`coordinator.md`), host configuration (`hosts-and-sessions.md`), executor practices (the `engine` skill).

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

`engine task complete-external <task-id> --version <n> --pr <url>` records work finished outside engine on a plan's task. It is operator-only, for an inactive task in todo or failed with no unresolved question. It takes a PR URL in the task's repository, does not verify the merge, records done and the PR in versioned history, keeps dependencies and prior attempts, and needs no attempt. Running and in-review still require an execution for ordinary transitions.

## API and storage

`POST /v1/command` accepts the Request in `internal/control/types.go`. Unknown fields, missing credentials and unknown operations fail; the credential sets authorship. Errors include an HTTP status; storage details stay in server logs.

Every write has a request key; a key reused for a different command fails. The CLI retries uncertain delivery once with the same key. Version conflicts require reading the current record. An edit invalidates a placement decision before any execution is reserved or launched.

SQLite runs locally through its CLI, with SQL on stdin; one server owns a database. Each transaction updates current records, history and its request receipt together. An older database migrates on open; an unknown schema version is rejected.

The operator credential is an owner-only regular file. Every dispatch creates a separate execution credential, scoped to that plan or task; workers cannot approve, start coordination or read the operator inbox. Ending an attempt revokes its credential. Credentials are hashed for lookup; dispatch receipts retain the token privately for interrupted dispatch recovery.

## Updates and operator inbox

Updates are immutable and ordered; read up to 200 after a cursor. Comments leave workflow state alone. A question atomically holds a task in `needs-human`: its own, or the asking task (the update's `task`) when a task's session asks on its plan. A task's session asks only on its task or plan. Acknowledgements and replies leave it unresolved. Resolution records the decision and atomically restores each held task's earlier state, unless another question holds it, its state changed or its execution ended. A task's own questions, including one it asked on its plan, block its state changes, completion, merge, wakes and automatic quota resume. A plan's question blocks planner handoff and the first dispatch of its tasks only: finished tasks still reach `in-review` and merge, and a task with an earlier attempt is dispatched again.

Messages addressed to the operator appear in the cross-repository inbox. Informational messages leave the inbox on acknowledgement; questions leave on resolution. Idle, unrestricted sessions get wake-up pointers for others' messages on their item; tasks also for others' plan messages and resolutions, except coordinator notices and those to the operator. Messages persist when wake-up fails.

## Dashboard

`engine ui` serves a read-only dashboard on `127.0.0.1:7332`; `--listen` chooses another loopback address. It uses the CLI's server configuration and operator credential, which never reaches the browser. Any local user reaching the port reads with operator rights; run it only on a single-user machine.

The process forwards only read operations (`list`, `plan-view`, `attempts`, `get`, `updates`, `session-view`) and answers GET and HEAD for a loopback host name on any port, so an SSH tunnel works. It sends `Cache-Control: no-store`. When the control server fails, the page keeps its last data, marked stale. Descriptions render as CommonMark plus tables: raw HTML stays text, images show alt text, and only absolute http, https and mailto links act. Messages are text; the CSP allows only the page's scripts.

The per-plan view lists unfinished prerequisites, including other plans' tasks. A queued task's feedback is the plan's question. On both tabs, "Needs feedback" opens the question's thread at its update over the tab, on the plan's thread when it asked (`internal/ui/static/sessions.js`).

Each task has one summary: done is done; failed and needs-human are blocked; an active attempt, or a running or in-review state, is in progress; an unfinished prerequisite or the plan's open question (as sessions report it) blocks it; todo under an approved plan is ready; anything else is blocked. Coordination does not change it.

## Session view

`session-view`, an operator-only read, returns one row per item's current, unended or vanished attempt, by item number. The fields in `internal/control/sessions.go` are the whole contract; credentials, screens, run files and bodies never join them.

- `role` is `planner` for a plan and `executor` for a task.
- `last_seen` is when a host last reported the window live; it survives `unknown` and `exited` observations.
- `observed` is the harness's `working`, `waiting` or `limited`; `live` for an open window with no harness state; `exited` once a reachable host shows the window gone; otherwise `unknown`. An exited attempt stays listed until engine ends it.
- `feedback` is the item's unresolved question: its own, or its plan's while the task waits to start. Waiting without one is not a feedback request.
- `latest` is the item's newest `say`, `ask`, `reply` or `resolved` update with a body, excluding coordinator notices. A task's preview never shows its plan's messages.

Observations live in an in-memory cache keyed by attempt, so a request never contacts a host. Coordinator passes store their host reads, and a background loop refreshes stale ones, one read in flight per host. The cache drops ended and replaced attempts and starts empty with the server. An unreachable host, unreadable run records or an old observation give `unknown`; the timings are in `internal/controlrun/observe.go`.
