# Coordinator

> **Owns:** the coordinator's pass: what triggers it, dispatch, recovery, the merge queue, how it reaches hosts, reads session signals and recovers limits. **Not here:** the records, API, credentials and inbox it reads and writes (`control-server.md`), host config and session access (`hosts-and-sessions.md`), the protocol it drives (`execution-protocol.md`).

## Shape

- **No agent.** The coordinator, inside `engine serve`, never edits code.
- **Stateless.** Each pass derives everything from the server's records and
  each host's tmux sessions, harness observations and run directories.
- **One pass** runs every 30 seconds and once at start, reading every host's
  `status` in parallel (`internal/reconcile`). Passes run the queues first; the session view's observer
  reuses those reads (`control-server.md`, "Session view"). Stopping
  coordination leaves sessions running. Plans move only to `planned` and `done`.

For harness scoring and host eligibility, load `hosts-and-sessions.md`,
"Placement". For changes to lint, load
`plugins/engine/skills/engine/references/plan.md`.

## Dispatch and recovery

Plans are serialized against fresh fleet capacity. A dispatch records its
attempt, host and credential before calling the host. A restart adopts
matching sessions or retries the same dispatch identity on that host. A
dispatch for a task whose earlier attempt ran is a resume, on the task's
branch from `origin` (`hosts-and-sessions.md`). A new attempt first releases
the ended one's session, once a read shows its window gone. A refused dispatch
retries each pass, noticing once per attempt.

The host serializes dispatches by run ID and returns an existing run on
replay. It uses the host's Git credentials and identity, with isolated clones
under `~/.local/share/engine/control-repos`. Credentials travel over SSH stdin
into an owner-only run file whose path the window command exports; hooks
recover their connection from the worktree's server-session record. Credential
files are never collected.

A window gone from a host that answered ends its attempt `vanished` if the
task is resumable: coordination stopped after the window was last seen live,
or the window was seen live and the task has a PR or open question, unless it
resumed a vanished attempt. The task keeps its state and resumes once any
question is resolved. Otherwise attempt and task end `failed`.
`engine attempt reconcile <id> --body <evidence>` is operator-only: it
requires a successful host read with no live matching window and a recorded
run, then applies the same rule. Unknown dispatch outcomes stay reserved.

## Merge queue

Each repository has one merge queue across its plans. `engine task
merge-ready`, for the in-review PR at its reviewed, pushed head, queues the
task; a later request keeps the place.

Only the queue's head is called. The head is the first eligible entry in
request order (in-review, coordinated, no unresolved question); it stays head
until it merges or leaves, and an entry that stops being eligible waits but
keeps its place. Waiting entries are never called, so a queued PR's changes are
found at its turn. The head is called through its task host's `merge` op,
because the server has no GitHub credential, at once when it becomes head or
requests again, then once per `CheckEvery` (`internal/reconcile/queue.go`). A
restart adds no call. A merge marks the task done with its PR. A head that is
behind, conflicting or moved gets `merge-turn`, waking the executor to refresh
and request again. A closed or branch-ruled head ends `merge-refused` and tells
the operator; a red head ends `merge-refused` without a notice. Every bounded-wait
refusal tells the operator, with the reason. A PR merged elsewhere is done, and
the operator told.

**After the merge**, the `merged` update gives the session a cleanup turn,
ending in `engine task cleaned`. Idle after that, it is released; `--left`
tells the operator. Without that, it is released once idle `CleanupWait` after
the merge, or at once if its window is gone, telling the operator where it ran;
a cleanup turn that stays busy at `CleanupCap` is noticed
(`internal/reconcile/session.go`). Other done tasks' sessions go once idle 10
minutes. Releasing a running background job is a notice.

Every wait is bounded. A head outstaying its bound, failing `gh` repeatedly,
or whose host is not in `hosts.toml` ends `merge-refused` with the reason; the
executor fixes it and requests again. Leaving in-review except for
`needs-human` withdraws the entry (`merge-withdrawn`); a vanished task resumes
in-review, keeping its entry. A lost merge result reads as merged.

## Reaching hosts

A host does its own work through `engine host <op> --json`: one job, one JSON
object on stdout, exit 0, or 1 on failure. The caller passes the model, effort
and kickoff; the host reads `hosts.toml` only for its launch line, harness
binary and slots. The coordinator calls its own host's ops in-process and
another machine's over `ssh` (`internal/remote/ssh.go`), and refuses to run
unless `~/.config/engine/host` names a machine in `hosts.toml`
(`hosts-and-sessions.md`). An SSH host needs key-based SSH, `engine`, `tmux`,
`gh` and the harness CLIs signed in; the coordinator and sessions use the
host's own `gh` and Git configuration (`operator-setup.md`).

| Op | Does |
| --- | --- |
| `control-spawn` | Syncs engine's clone, then runs `spawn` with the run's credentials. |
| `sync-repo` | Creates or fetches engine's clone; returns the default branch. |
| `spawn` | Starts a session; the kickoff arrives on stdin (`hosts-and-sessions.md`). |
| `status` | Windows with their state and, unless working, last 20 screen lines and a waiting window's newest denial (`telemetry.md`); newest runs; quota, free slots and signed-out harnesses (`internal/hostops/status.go`). |
| `collect` | Returns a run directory's index and file list; nothing is uploaded. |
| `merge` | Merges a PR pinned to the reviewed head, once it is open, green (no checks is green), mergeable, unblocked by branch rules and not behind its base. Already merged is done. |
| `release` | Closes the window and deletes each harness's files for it. For a done task, runs the repo's `dev worktree release <branch>` if provided (bounded; a failure is a note). Removes a remaining worktree unless the run failed or it has changes. Engine never deletes the branch. |

**The envelope.** Every reply, a failure too, carries `protocol`, `engine`
(the build) and `host`. A failure is `{"error": …, "refused": true|false}`;
`refused` means the op changed nothing. Over SSH, exit 255 is an unreachable
host. A caller refuses a reply on another protocol, or not an engine's,
telling the operator to run `engine setup --host <name>`.

**A failed call is unknown, never dead.** No session of a failed call or
unreachable host is marked dead, failed, stalled, restarted or reaped; only a
successful `status` lacking a window shows it gone.

## Session signals

Agent state is never scraped from the pane: task state and questions are
server records. Two sources say whether a session is alive and takes input:

1. **The harness** gives each window of session `engine` a `state` (`working`,
   `waiting` or `limited`) from the observation of the harness its newest
   `run.json` names (how each harness reads it: `harnesses.md`). A window with
   no run included is unknown.
2. **tmux.** A window whose command has not exited is alive; a dead window is
   dead whatever the harness says.

A session is **idle** when `waiting`, or `working` with `background` set. Only
idle sessions get wake-up pointers.

**Stalls.** Limited, queued and background-only sessions never stall. A
dispatched session that has not claimed its task 5 minutes after it started is
noticed once, and one that ended before claiming is restarted. A session
waiting 10 minutes on an open task with no hard stop or question has stopped
outside the protocol; the operator is notified once, naming the sign-in for a
signed-out harness. A denial at most 10 minutes older than the wait makes the
stall a Permission hard stop naming the tool, input and reason.

**Limit recovery.** Never resume `needs-human`; recheck the task's state
before typing. Each check types literal `continue` then Enter into a window
still `limited`, or `waiting` with a positive limit `resets_at`, once the
reset plus 2 minutes has passed; other sessions and unknown resets are left
alone. The attempt is noted first, so a failed Enter never duplicates text.
Waiting with a limit reset also clears the stall timer. Still `limited` 10
minutes after the attempt, or a reset over 24 hours away (no typing), gives one
notice per reset and session. Recovery covers planners, executors and cleanup
turns.
