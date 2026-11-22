# Coordinator

> **Owns:** the coordinator's pass: what triggers it, dispatch, recovery, the merge queue and how it reaches hosts. **Not here:** the records, API, credentials and inbox it reads and writes (`control-server.md`), host config and session access (`hosts-and-sessions.md`), session signals, stalls, vanishes and limit recovery (`session-signals.md`), the protocol it drives (`execution-protocol.md`).

## Shape

- **No agent.** The coordinator, inside `engine serve`, never edits code.
- **Stateless.** Each pass derives everything from the server's records and each host's tmux sessions, observations, run directories and readiness.
- **One pass** runs every 30 seconds and once at start, reading every host's `status` in parallel (`internal/reconcile`). Passes run the queues first; the session view's observer reuses those reads (`dashboard.md`). Stopping coordination leaves sessions running; plans move only to `planned` and `done`.

For harness scoring and host eligibility, load `hosts-and-sessions.md`, "Placement". For session liveness, stalls, vanishes, early deaths and limits, load `session-signals.md`.

## Dispatch and recovery

Plans serialize against fresh fleet capacity. A dispatch records its attempt, host and credential before calling the host. A restart adopts matching sessions or retries the same dispatch identity on that host. A dispatch for a task whose earlier attempt opened a window is a resume. Any re-dispatch opens on the task's own local branch if a run of the task marked it, else, on a resume, on `origin`'s (`hosts-and-sessions.md`). A new attempt first releases the ended one's session once its window is seen gone. A refused dispatch retries each pass, noticing once per attempt; an unresolved one, once per distinct first line of its error, tmux ids aside.

The host serializes dispatches by run ID and returns an existing run on replay. A spawn failing before its window opens removes what it made (the run directory; the worktree and branch unless reused), naming any leftover; a run directory with no `run.json` and no window is reused. It uses the host's Git credentials and identity, with isolated clones under `~/.local/share/engine/control-repos`. Credentials travel over SSH stdin into an owner-only run file whose path the window command exports; hooks recover their connection from the worktree's server-session record. Credential files are never collected.

A window gone from a host that answered ends its attempt by the vanish and early-death rules (`session-signals.md`, "Vanish" and "Early death").

A task the operator moved (`hosts-and-sessions.md`) waits until a pass's `release --moved` of its old run, window open or gone, succeeds, and while the old host is unread, unless it was moved blind; a wait on an unread host tells the operator once. Releasing work `origin` lacks tells the operator, unless the move stayed on that host. A blind move's old run is released when its host answers, a done plan's too.

Operator-only `engine attempt reconcile <id> <evidence>` applies the vanish and early-death rules once a successful host read shows a started attempt's window gone with its run recorded, or a `dispatching` attempt's with no window of its name and no run record (ending it vanished, its task back in `todo`). Otherwise, a planner's dispatch included, it refuses (`control-server.md`); unknown dispatch outcomes stay reserved. A spawn still in flight shows neither and may then open an orphan window, which blocks the next dispatch until removed.

## Merge queue

Each repository has one merge queue across its plans. `engine task merge-ready`, for the in-review PR at its reviewed, pushed head, queues the task; a later request keeps its place.

Only the queue's head is called: the first eligible entry in request order (in-review, coordinated, no unresolved question of its own). It stays head until it merges or leaves. An entry that stops being eligible waits, keeping its place, and is not called. The head is called through its task host's `merge` op, since the server has no GitHub credential: at once when it becomes head or requests again, then once per `CheckEvery` (`internal/reconcile/queue.go`). A restart adds no call. A merge marks the task done with its PR. A head that is behind, conflicting or moved gets `merge-turn`, waking the executor to refresh and request again. A closed or branch-ruled head ends `merge-refused` and tells the operator; a red head ends `merge-refused` without a notice. A PR merged elsewhere is done, and the operator told.

**After the merge**, the `merged` update gives the session a cleanup turn, ending in `engine task cleaned`. Idle after it, the session is released (`--left` tells the operator); without a cleanup, once idle `CleanupWait` after the merge, or at once if its window is gone, telling the operator where it ran. Busy at `CleanupCap`, it is released if cleaned, else noticed (`internal/reconcile/session.go`). Other done tasks' sessions go once idle 10 minutes. Releasing a running background job is a notice.

Every wait is bounded. A head outstaying its bound, failing `gh` repeatedly, or whose host is not in `hosts.toml` ends `merge-refused`, telling the operator why; the executor fixes it and requests again. Leaving in-review except for `needs-human` withdraws the entry (`merge-withdrawn`); a vanished task resumes in-review, keeping its entry. A lost merge result reads as merged.

## Reaching hosts

A host does its own work through `engine host <op> --json`: one job, one JSON object on stdout, exit 0, or 1 on failure. The caller passes the model, effort and kickoff; the host reads only its `hosts.toml` launch line, harness binary, `path` and slots. The coordinator runs its own host's ops in-process, another machine's over `ssh` (`internal/remote/ssh.go`), and refuses to run unless `~/.config/engine/host` names a `hosts.toml` machine (`hosts-and-sessions.md`).

| Op | Does |
| --- | --- |
| `control-spawn` | Syncs engine's clone, then runs `spawn` with the run's credentials. |
| `sync-repo` | Creates or fetches engine's clone; returns the default branch. |
| `spawn` | Starts a session; the kickoff arrives on stdin (`hosts-and-sessions.md`). |
| `status` | Windows with their state and screen tail, recent runs, quota, free slots and signed-out harnesses (`telemetry.md`, `internal/hostops/status.go`). |
| `collect` | Returns a run directory's index and file list; nothing is uploaded. |
| `merge` | Merges a PR pinned to the reviewed head once it is open, green (no checks is green), mergeable, unblocked by branch rules and not behind its base. Per check name, the latest start counts. Pending waits; failure refuses. Commit subject: PR title plus number; body: PR body. |
| `ready` | Probes a repo's readiness (`readiness.md`). |
| `typed` | Logs a text's hash before the coordinator types it (`pane-input.md`). |
| `release` | Closes the window and deletes its harness files. For a done task, runs the repo's `dev worktree release <branch>` if provided. Removes the worktree unless the run failed, was stopped or moved, hit a usage limit again, or has changes; a moved run's branch is first pushed, without force, and the reply says what `origin` lacks; deletes the local branch if its tip is the merged head (`--head`) or in `origin`'s default branch after a fetch, else notes the unmerged commits. Other releases keep the branch. |

**The envelope.** Every reply, a failure too, carries `protocol`, `engine` (the build) and `host`. A failure is `{"error": …, "refused": true|false}`; `refused` means the op changed nothing. Over SSH, exit 255 is an unreachable host. A caller refuses a reply on another protocol, or not an engine's, and tells the operator to run `engine setup --host <name>`.

**A failed call is unknown, never dead.** No session of a failed call or unreachable host is marked dead, failed, stalled, restarted or reaped; only a successful `status` lacking a window shows it gone.
