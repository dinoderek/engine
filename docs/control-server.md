> **Owns:** server deployment, records, API, credentials, the inbox, the dashboard and the session view. **Not here:** dispatch, recovery, the merge queue, host calls and session signals (`coordinator.md`), host configuration (`hosts-and-sessions.md`), executor practices (the `engine` skill).

# Control server

`engine serve` owns plans, tasks, updates, execution attempts and merge queue
entries, and runs the coordinator. GitHub supplies code, PRs and CI; task state and
comms stay in the server. For dispatch, recovery and merges, load
`coordinator.md`.

## Start

The server needs `sqlite3` and `tmux`. Each operator's
`~/.config/engine/config.toml` names its host from `hosts.toml`
(`[server] host = "devbox"`):

```sh
mkdir -p ~/.config/engine
(umask 077; openssl rand -hex 32 > ~/.config/engine/server-token)
engine serve
```

`engine serve` starts the server on that host (over ssh if remote) and
returns. It runs in the private tmux server `engine-server`
(`tmux -L engine-server attach`), logging to
`~/.local/share/engine/server/serve.log`, on port 7331 of the Tailscale name
in the host's address: the origin it advertises and the CLI uses. The tailnet
keeps credentials confidential. To restart, close its window and rerun.

`--foreground` runs it in place. For other networks, use an SSH tunnel or a
TLS reverse proxy, set `[server] url` instead of `host`, and use
`--foreground`. Without config.toml, the CLI uses `http://devbox:7331`;
`ENGINE_SERVER_URL` overrides both. The CLI reads its credential from
`~/.config/engine/server-token` or `ENGINE_SERVER_TOKEN_FILE`; workers get the
advertised URL.

For backup, stop the server, run `engine serve --backup <new-path>`, restart
it, and copy the backup off-machine. Restore with the server stopped, keeping
the operator credential. Backups hold credentials, are private and never
overwrite a file. Test restores in an isolated database first.

## Records and commands

IDs belong to engine: repositories, plans, tasks and attempts have separate
prefixes and globally unique numeric parts. A plan is a feature-level
requirements/design document. Every task belongs to one plan; creating a task
without `--plan` fails. `engine help` lists the command groups; the flags are
in `internal/controlcmd/command.go`.

Edits, approvals and workflow changes require the current `--version`. A
plan's edit invalidates its approval; an active execution, a plan's children
included, prevents an edit. Approve, run and stop take a plan and persist its
coordination intent (`coordinator.md`). Retrying a failed task is an explicit
operator state change. `get`, `list` and `plan-view` add a plan's derived,
unstored `status` (`execution-protocol.md`, "States").

`engine lint <plan-id>` checks a plan's tasks before approval and changes
nothing; it reads one snapshot, including outside dependencies.

`engine task complete-external <task-id> --version <n> --pr <url>` records
work finished outside engine on a plan's task. It is operator-only, for an
inactive task in todo or failed with no unresolved question, and it does not
verify the merge. It records done and the PR in versioned history, keeps
dependencies and prior attempts, and needs no attempt. Ordinary state
transitions still require an execution for running and in-review.

## API and storage

`POST /v1/command` accepts the Request in `internal/control/types.go`.
Unknown fields, missing credentials and unknown operations fail; the
credential sets authorship. Storage details remain in server logs.

Every write has a request key; a key reused for a different command fails.
The CLI retries uncertain delivery once with the same key. Version conflicts
require reading the current record. An edit invalidates a placement decision
before any execution is reserved or launched.

SQLite runs locally through its CLI, with SQL on stdin; one server owns a
database. Each transaction updates current records, history and its request
receipt together. An older database migrates on open; an unknown schema
version is rejected.

The operator credential is an owner-only regular file. Every dispatch creates
a separate execution credential, scoped to that plan or task. Workers cannot
approve, start coordination or read the operator inbox. Ending an attempt
revokes its credential. Credentials are hashed for lookup; dispatch receipts
retain the token privately for interrupted dispatch recovery.

## Updates and operator inbox

Updates are immutable and ordered; read up to 200 after a cursor. Comments
leave workflow state alone. A question atomically holds a task in
`needs-human`: its own, or the asking task (the update's `task`) of one a
task's session asks on its plan. A task's session asks only on its task or
plan. Acknowledgements and replies leave it unresolved. Resolution records the
decision and atomically restores each held task's earlier state, unless
another question holds it, its state changed or its execution ended.
Unresolved questions block completion and merge checks; a plan's also block
its tasks' state changes, planner handoff, child dispatch and automatic quota
resume.

Messages addressed to the operator appear in the cross-repository inbox.
Informational messages leave the inbox on acknowledgement; questions leave on
resolution. Idle, unrestricted sessions get wake-up pointers for others'
messages on their item; tasks also for others' plan messages and resolutions,
except coordinator notices and those to the operator. Messages persist when
wake-up fails.

## Dashboard

`engine ui` serves a read-only dashboard on `127.0.0.1:7332`; `--listen`
chooses another loopback address. It uses the CLI's server configuration and
operator credential, which never reaches the browser. Any local user reaching
the port reads with operator rights; run it only on a single-user machine.

The process forwards only read operations (`list`, `plan-view`, `attempts`,
`get`, `updates`, `session-view`) and answers GET and HEAD for a loopback
host name on any port, so an SSH tunnel works. It sends
`Cache-Control: no-store`. When the control server fails, the page keeps its
last data, marked stale. Descriptions render as CommonMark plus tables: raw
HTML stays text, and only absolute http, https and mailto links act. Messages
are text; the CSP allows only the page's scripts.

Each task has one summary. Done is done. Failed and needs-human are blocked.
An active attempt, running or in-review, is in progress. Otherwise an
unfinished prerequisite or the plan's open question (as sessions report it)
blocks it, todo under an approved plan is ready to run, and anything else is
blocked. Coordination does not change it.

## Session view

`session-view` is an operator-only read: one row per item's current, unended
or vanished attempt, ordered by item number. The row fields in
`internal/control/sessions.go` are the whole contract; credentials, screens,
run files, item bodies and full update bodies never join them.

- `role` is `planner` for a plan and `executor` for a task.
- `observed` is the harness's `working`, `waiting` or `limited`; `live` for an
  open window without a harness state; `exited` once a reachable host shows the
  window closed or gone; otherwise `unknown`. An exited attempt stays listed
  until engine ends it.
- `feedback` is the item's unresolved question, its own or its parent plan's.
  Waiting without one is not a feedback request.
- `latest` is the item's newest `say`, `ask`, `reply` or `resolved` update with
  a body, excluding the coordinator's automatic notices. A task's preview never
  shows its plan's messages.

Observations live in an in-memory cache keyed by attempt, so a request never
contacts a host. Coordinator passes store their host reads; a background loop
reads each host whose active attempts lack a fresh observation. An unreachable
host, unreadable run records or an old observation give `unknown`. The cache
starts empty with the server; its timings are the constants in
`internal/controlrun/observe.go`.
