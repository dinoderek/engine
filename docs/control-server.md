> **Owns:** server deployment, records, API, credentials, the inbox, the dashboard and the session view. **Not here:** dispatch, recovery, merges and merge locks, host calls and session signals (`coordinator.md`), host configuration (`hosts-and-sessions.md`), executor practices (the `engine` skill).

# Control server

`engine serve` owns plans, tasks, updates, execution attempts and merge locks,
and runs the coordinator. GitHub supplies code, PRs and CI; task state and
comms stay in the server. For dispatch, recovery, merges and locks, load
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

`--foreground` runs it in place. For other networks, use an SSH tunnel or a TLS reverse proxy, set
`[server] url` instead of `host`, and use `--foreground`. Without
config.toml, the CLI uses `http://devbox:7331`; `ENGINE_SERVER_URL` overrides
both. The CLI reads its credential from `~/.config/engine/server-token` or
`ENGINE_SERVER_TOKEN_FILE`; workers get the advertised URL.

For backup, stop the server, run `engine serve --backup <new-path>`, restart
it, and copy the backup off-machine. Restore with the server stopped,
keeping the operator credential. Backups hold credentials, are private
and never overwrite a file. Test restores in an isolated database first.

## Records and commands

IDs belong to engine: repositories, plans, tasks and attempts have separate
prefixes and globally unique numeric parts. A plan is a feature-level
requirements/design document. Standalone tasks have no parent plan.
`engine ps` lists server tasks. `engine help` lists server command groups; the
source of the flags is `internal/controlcmd/command.go`.

```sh
engine repo add --title Demo --slug owner/demo --remote git@github.com:owner/demo.git
engine plan create --repo r1 --title Feature --body-file requirements.md
engine task create --repo r1 --plan p2 --title Implementation --body-file card.md
engine plan show p2
engine lint p2
engine plan approve p2 --version 1
engine run p2
engine task list p2
engine attempt list t3
```

Dependencies, capabilities, harness and tier are
structured fields, supplied with `--depends`, `--requires`, `--harness`
and `--tier`.

Edits, approvals and workflow changes require the current `--version`. An
edit invalidates a plan's or standalone task's approval; an active
execution, a plan's children included, prevents it. Run/stop commands persist
coordination intent (`coordinator.md`).
Retrying a failed task is an explicit operator state change.

`engine lint <plan-id>` checks a server plan's tasks before approval:
Outcome and Tests sections, no cycle, one acceptance task, one owner per
Open question. It reads one snapshot, including outside dependencies, and
changes nothing.

`engine task complete-external <task-id> --version <n> --pr <url>` records
work finished outside engine. It is operator-only, for an inactive task in
todo, prepare, approved or failed with no unresolved question, and takes a
PR URL in the task's repository without verifying the merge. It records done
and the PR in versioned history, stops coordination for the task, keeps
dependencies and prior attempts, and needs no approval or attempt.
Ordinary state transitions still require an execution for running and
in-review.

## API and storage

`POST /v1/command` accepts the Request in `internal/control/types.go`.
Unknown fields, missing credentials and unknown operations fail; the
credential sets authorship. Errors include an HTTP
status; storage details remain in server logs.

Every write has a request key; a key reused for a different command fails.
The CLI retries uncertain delivery once with the same key; `--request-key`
sets one. Version conflicts require reading the current record.
An edit invalidates a placement decision before any execution is reserved or
launched.

SQLite runs locally through its CLI, with SQL on stdin; one server owns a
database. Each transaction updates current records, history and its request
receipt together. The schema is versioned; unknown versions are rejected.

The operator credential is an owner-only regular file. Every dispatch creates
a separate execution credential, scoped to that plan or task. Workers cannot
approve, start coordination, read the operator inbox or recover locks.
Ending an attempt revokes its credential. Credentials are hashed for lookup;
dispatch receipts retain the token privately for interrupted dispatch recovery.

## Updates and operator inbox

```sh
engine tell t3 "Use the second approach"
engine updates t3 --after 0
engine ask t3 "Approve this API change?"
engine ask p2 "The unit lane is red on main"
engine inbox --operator
engine ack 12
engine reply 12 "Investigating"
engine resolve 12 "Approved for the task revision in this question"
```

Updates are immutable and ordered; read up to 200 after a cursor.
Comments leave workflow state alone. A question atomically holds a task in
needs-human: its own, or the asking task (the update's `task`) of one a
task's session asks on its plan. A task's session asks only on its task or plan. Acknowledgements
and replies leave it unresolved. Resolution records the decision and atomically
restores the held task's earlier state, unless another question holds it,
its state changed or its execution ended. Unresolved
questions block completion and merge checks; a plan's also block its tasks'
state changes, planner handoff, child dispatch and automatic quota resume.

Messages addressed to operator appear in the cross-repository inbox.
Informational messages leave the inbox on acknowledgement; questions leave
on resolution. Waiting, unrestricted sessions get wake-up pointers for
others' messages on their item; tasks also for others' plan messages and
resolutions, except coordinator notices and those to the operator. Messages
persist when wake-up fails.

## Dashboard

`engine ui` serves a read-only dashboard on `127.0.0.1:7332`; `--listen`
chooses another loopback address. It uses the CLI's server configuration and
operator credential, and the credential never reaches the browser. Any local
user reaching the port reads with operator rights; run it only on a
single-user machine. The page calls fixed same-origin routes:

- `/api/plans`: repositories and plans, filtered by `repo`, `state` and a
  case-insensitive `q` over plan ID, repository and title.
- `/api/plans/<id>`: the plan's direct tasks with summary counts, hosts from
  active attempts, unfinished prerequisites, including other plans' tasks, and
  `feedback`: the session's, or the plan's question for a queued task.
- `/api/sessions`: the `session-view` rows, feedback requests first.
- `/api/items/<id>` and `/thread`: a plan or task with its description
  rendered, and its messages, 50 after `after` (through `anchor`), with a
  cursor and question statuses.

Descriptions support headings, flat lists, code, links. Raw HTML and
messages are text; only absolute http, https and mailto links act.

The process forwards only the `list`, `plan-view`, `attempts`, `get`,
`updates` and `session-view` reads. It answers GET and HEAD for a loopback
host name on any port, so an SSH tunnel works, and sends
`Cache-Control: no-store`. When the control server fails, the page keeps its
last data, marked stale.

On both tabs, "Needs feedback" opens the question's thread at its
update over the tab, on the plan's thread when it asked.

Each task has one summary. Done is done. Failed and needs-human are blocked.
An active attempt, running or in-review is in progress. Otherwise an
unfinished prerequisite or the plan's open question (as sessions report
it) blocks it, todo or approved under an approved plan is ready to run, and
anything else is blocked.
Coordination does not change it.

## Session view

`session-view` is an operator-only read. It returns `sessions`: one row per item's current, unended or vanished attempt,
ordered by item number. The row and reference fields in
`internal/control/sessions.go` are the whole contract: credentials, screens,
run files, item bodies and full update bodies never join them.

- `role` is `planner` for a plan and `executor` for a task. `plan` is the
  planner's own plan or a child task's parent; a standalone task has none.
- `observed` is the harness's `working`, `waiting` or `limited`, `live` for
  an open window without a harness state, `exited` once a reachable host shows
  the window closed or gone, and otherwise `unknown`. An exited attempt stays
  listed until engine ends it.
- `last_seen` is when a host last reported the window live; it survives
  unknown and exited observations.
- `feedback` is the item's unresolved question, its own or its parent plan's.
  Acknowledgements and replies leave it; resolution clears it. Waiting
  without one is not a feedback request.
- `latest` is the item's newest `say`, `ask`, `reply` or `resolved` update
  with a body, excluding the coordinator's automatic notices. A task's
  preview never shows its plan's messages.

A reference's `update` is the `engine updates` sequence; its `summary` is one
plain-text line.

Observations live in an in-memory cache keyed by attempt, so a request never
contacts a host. Coordinator passes store their host reads; a background
loop reads each host whose active attempts lack a fresh observation, one
read in flight per host.
An unreachable host, unreadable run records or an old observation give
`unknown`. The cache drops ended and
replaced attempts and starts empty with the server. The timings are the
constants in `internal/controlrun/observe.go`.
