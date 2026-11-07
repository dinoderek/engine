> **Owns:** server deployment, records, API, credentials, inbox, coordination recovery and repository merge locks. **Not here:** host configuration (`hosts-and-sessions.md`), GitHub-mode coordination (`coordinator.md`), executor practices (the engine skill).

# Control server

`engine serve` owns plans, tasks, updates, execution attempts and merge locks.
The CLI uses its authenticated command API. The server coordinates through
the existing reconcile decisions and host operations. Executors edit, test,
review and merge. GitHub supplies code, PRs and CI; task state and comms stay
in the server. A feature's GitHub issue can hold a human-maintained PR summary.
There is no automatic tracker migration or summary synchronisation.

## Start

The server requires `sqlite3`. Run one server on `devbox` and give every
CLI and execution host access to its origin. Bind port 7331 to the devbox's
Tailscale address and advertise its MagicDNS origin. The tailnet provides
confidential transport for bearer credentials. For other networks, use an
SSH tunnel or a TLS reverse proxy.

```sh
mkdir -p ~/.config/engine ~/.local/share/engine/server
(umask 077; openssl rand -hex 32 > ~/.config/engine/server-token)
engine serve --db ~/.local/share/engine/server/control.db \
  --token-file ~/.config/engine/server-token \
  --listen "$(tailscale ip -4):7331" --advertise http://devbox:7331
```

Each operator CLI reads `~/.config/engine/config.toml`:

```toml
[server]
url = "http://devbox:7331"
```

Without that file, the CLI uses the origin shown above. An explicit
`ENGINE_SERVER_URL` overrides it. Provision the operator credential in
`~/.config/engine/server-token` on each operator machine, or set
`ENGINE_SERVER_TOKEN_FILE` to its path. Workers receive their scoped
credential and the advertised URL at dispatch. Host definitions remain
in the existing host config.

For backup, stop the server, run the same invocation with
`--backup <new-path>`, and restart it. Copy the backup off-machine. Restore
with the server stopped, retaining the private operator credential. A backup
includes execution credentials and is private. The backup operation refuses
to overwrite an existing file. Test restores into an isolated database before
replacing the active database.

## Records and commands

IDs belong to engine: repositories, plans, tasks and attempts have separate
prefixes and globally unique numeric parts. A plan is a feature-level
requirements/design document. Standalone tasks have no parent plan.
Commands return JSON. `engine ps` lists server tasks. Use `engine ps --github`
for GitHub claims. `engine help` lists server command groups; the source
of the flags is `internal/controlcmd/command.go`.

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

Substitute returned IDs and versions. Task cards carry the skill's Outcome
and Tests sections. Dependencies, capabilities, harness and tier are
structured fields, supplied with `--depends`, `--requires`, `--harness`
and `--tier`. The server adapter supplies card keys and maps these fields
to plan lint and placement.

Edits replace the card's editable fields and require its current
`--version`. Plan and standalone-task edits invalidate approval.
Active executions, including a plan's children, prevent card edits.
Approval and workflow changes require a version. Run/stop commands persist
coordination intent; stopping coordination leaves existing sessions running.
Retrying a failed task is an explicit operator state change.

`engine lint <plan-id>` checks the current server plan before approval using
the coordinator's plan projection and the same rules as GitHub lint. It
reads one credential-scoped snapshot, including dependencies outside the
plan, without changing approval, workflow, history or host observations.

`engine task complete-external <task-id> --version <n> --pr <url>` records
work finished outside engine. Only the operator can use it, for an inactive
task in todo, prepare, approved or failed, with no unresolved task or plan
question. The URL must identify a PR in the task's GitHub repository; engine
records the operator's evidence without fetching or verifying its merge.
The command stores done and the PR in versioned history, stops coordination
for that task and preserves dependency edges and prior attempts. It creates
no attempt and requires no approval. Ordinary state transitions still require
an execution for running and in-review.

## API and storage

`POST /v1/command` accepts the Request in `internal/control/types.go`.
Unknown JSON fields, missing credentials and unsupported operations fail.
The server derives authorship from the credential. Errors include an HTTP
status; storage details remain in server logs.

Every write has a request key. Retries use exactly the same command and key.
A reused key with different contents fails. The CLI retries uncertain
delivery once with the original key; callers can retain one with
`--request-key`. Version conflicts require reading the current record.
Dispatch reservations validate the card version used for placement; an edit
invalidates the decision before any execution is reserved or launched.

SQLite runs locally through its CLI, with SQL on stdin. A process lock
allows one engine server per database; a mutex serializes commands.
Each transaction updates current records, immutable history and its request
receipt together. History retains record snapshots. The database uses a
versioned schema and rejects unknown versions. This small-store implementation
loads its control records for each operation; bulk telemetry belongs elsewhere.

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
engine inbox --operator
engine ack 12
engine reply 12 "Investigating"
engine resolve 12 "Approved for the task revision in this question"
engine task state t3 --version 4 --state running
```

Use returned sequences and versions. Updates are immutable and ordered.
Read up to 200 at a time, using the last sequence as the next cursor.
Comments do not change workflow state. Asking a task question atomically
puts it in needs-human. Acknowledgement and ordinary replies leave the
question unresolved. Resolution records the operator decision; an explicit
state change resumes the task. Unresolved questions block completion and
merge checks.
An unresolved plan question also blocks planner handoff, child dispatch,
child merges and automatic quota resume until the operator resolves it.

Messages addressed to operator appear in the cross-repository inbox.
Informational messages leave the inbox on acknowledgement; questions leave
on resolution. Messages may omit a task association for host or server
problems. Wake-up pointers are attempted for waiting, unrestricted sessions;
messages persist when wake-up fails. Phone push is outside this
implementation.

## Dashboard

`engine ui` serves a read-only dashboard on `127.0.0.1:7332`; `--listen`
chooses another loopback address. It uses the CLI's server configuration and
operator credential, and the credential never reaches the browser. The page
calls fixed same-origin routes:

- `/api/plans`: repositories and plans, filtered by `repo`, `state` and a
  case-insensitive `q` over plan ID, repository and title.
- `/api/plans/<id>`: the plan's direct tasks with summary counts, hosts from
  active attempts and unfinished prerequisites, including other plans' tasks.

The process forwards only the `list`, `plan-view` and `attempts` reads. It
answers GET and HEAD for a loopback host name on any port, so an SSH tunnel
works, and sends `Cache-Control: no-store`. When the control server fails,
each route returns its last successful data marked stale. The page refreshes
about every 30 seconds while visible.

Each task has one summary. Done is done. Failed and needs-human are blocked.
An active attempt, running or in-review is in progress. Otherwise an
unfinished prerequisite blocks it, todo or approved under an approved plan
is ready to run, and anything else waits for the operator, so is blocked.

## Dispatch and recovery

The server periodically reconciles persisted coordination intent. It shares
placement decisions with the CLI coordinator and serializes plans against
fresh fleet capacity. A dispatch records its attempt, host and credential
before calling the host. Restart adopts matching sessions or retries the
same dispatch identity on that host. An unreachable host means unknown,
never dead.

The host serializes dispatches by run ID and returns an existing run on
replay. It uses ordinary Git credentials and identity, with isolated clones
under `~/.local/share/engine/control-repos`. Credentials travel through
SSH stdin into an owner-only run file. The window command exports the file's
path; joined Codex sessions receive that path too. The server-session record
lets hooks recover their connection from a worktree. Credential and lock-token
files are excluded from collected artifacts.

`engine attempt reconcile <id> --body <evidence>` is operator-only. The
server requires a successful host observation with no live matching window
and a recorded run. Unknown dispatch outcomes remain reserved. Ending an
unfinished task's attempt marks it failed. Inspect the PR outcome before
recovering a retained merge lock.

## Merge locks

One repository lock covers every plan and standalone task. Acquisition is
atomic and records the execution owner and a fresh token. Locks have no
expiry. The executor holds its lock through the final default-branch update,
gates, review/check validation and pinned-head merge.

`engine lock acquire` records the token privately in the current run
directory. The pre-tool hook checks ownership before a GitHub PR merge,
including safe pinned merges. A failed server check blocks merging.
`engine lock release` requires the owner's token. A delayed release cannot
remove another attempt's lock.

After checking the host and PR outcome, the operator can inspect
`engine lock show <repo-id>` and use `engine lock recover` with the current
attempt, its token file and reconciliation evidence. Recovery requires an
ended attempt. Manual merges outside engine can still move the default branch.
