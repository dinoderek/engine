# Coordinator

> **Owns:** the coordinator's pass: what triggers it, dispatch, recovery, the merge queue and how it reaches hosts. **Not here:** the records, API, credentials and inbox it reads and writes (`control-server.md`), host config and session access (`hosts-and-sessions.md`), session signals, stalls and limit recovery (`session-signals.md`), the protocol it drives (`execution-protocol.md`).

## Shape

- **No agent.** The coordinator, inside `engine serve`, never edits code.
- **Stateless.** Each pass derives everything from the server's records and each host's tmux sessions, harness observations and run directories.
- **One pass** runs every 30 seconds and once at start, reading every host's `status` in parallel (`internal/reconcile`). Passes run the queues first; the session view's observer reuses those reads (`control-server.md`, "Session view"). Stopping coordination leaves sessions running; plans move only to `planned` and `done`.

For harness scoring and host eligibility, load `hosts-and-sessions.md`, "Placement". For how a pass reads a session's liveness, stalls or limits, load `session-signals.md`. For lint changes, load `plugins/engine/skills/engine/references/plan.md`.

## Dispatch and recovery

Plans are serialized against fresh fleet capacity. A dispatch records its attempt, host and credential before calling the host. A restart adopts matching sessions or retries the same dispatch identity on that host. A dispatch for a task whose earlier attempt ran is a resume, on the task's branch from `origin` (`hosts-and-sessions.md`). A new attempt first releases the ended one's session, once a read shows its window gone. A refused dispatch retries each pass, noticing once per attempt; an unresolved one, once per distinct first line of its error, tmux ids aside.

The host serializes dispatches by run ID and returns an existing run on replay. A spawn failing before its window opens removes what it made (the run directory; the worktree and branch unless reused), naming any leftover; a run directory with no `run.json` and no window is reused. It uses the host's Git credentials and identity, with isolated clones under `~/.local/share/engine/control-repos`. Credentials travel over SSH stdin into an owner-only run file whose path the window command exports; hooks recover their connection from the worktree's server-session record. Credential files are never collected.

A window gone from a host that answered ends its attempt `vanished` if the task is resumable: coordination stopped after the window was last seen live, or it was seen live and the task has a PR or open question, unless it resumed a vanished attempt. The task keeps its state and resumes once any question is resolved. Otherwise attempt and task end `failed`, except an early death:

**Early death.** A window gone before its session claimed the task, which neither wrote to the server nor was seen live for `ClaimWait`, is not a vanish unless the vanish rule above already applies. The first such death per task, across attempts, ends the attempt `early-death` with its pane log tail recorded; a running task goes back to `todo` and the next pass dispatches a new attempt, with a notice. The second ends the attempt and task `failed`, and its notice carries both attempts' tails. Deaths of sessions that claimed never count; an early death after one is still the first. An operator's retry of a failed task counts the earlier early deaths, so one more fails it at once.

Operator-only `engine attempt reconcile <id> <evidence>` needs a successful host read showing, for a started attempt, no live matching window and a recorded run, then applies the vanish and early-death rules; for a task's `dispatching` one, no window of its name and no run record, ending it vanished with its task back in `todo`. A spawn still in flight shows neither, so it may then open an orphan window, which blocks the next dispatch until removed. Otherwise, a planner's dispatch included, it refuses (`control-server.md`); unknown dispatch outcomes stay reserved.

## Merge queue

Each repository has one merge queue across its plans. `engine task merge-ready`, for the in-review PR at its reviewed, pushed head, queues the task; a later request keeps its place.

Only the queue's head is called: the first eligible entry in request order (in-review, coordinated, no unresolved question of its own). It stays head until it merges or leaves. An entry that stops being eligible waits, keeping its place, and is not called. The head is called through its task host's `merge` op, since the server has no GitHub credential: at once when it becomes head or requests again, then once per `CheckEvery` (`internal/reconcile/queue.go`). A restart adds no call. A merge marks the task done with its PR. A head that is behind, conflicting or moved gets `merge-turn`, waking the executor to refresh and request again. A closed or branch-ruled head ends `merge-refused` and tells the operator; a red head ends `merge-refused` without a notice. A PR merged elsewhere is done, and the operator told.

**After the merge**, the `merged` update gives the session a cleanup turn, ending in `engine task cleaned`. Idle after that, it is released; `--left` tells the operator. Without that, it is released once idle `CleanupWait` after the merge, or at once if its window is gone, telling the operator where it ran. A cleanup turn still busy at `CleanupCap` is noticed (`internal/reconcile/session.go`). Other done tasks' sessions go once idle 10 minutes. Releasing a running background job is a notice.

Every wait is bounded. A head outstaying its bound, failing `gh` repeatedly, or whose host is not in `hosts.toml` ends `merge-refused` with the reason, which the operator is told; the executor fixes it and requests again. Leaving in-review except for `needs-human` withdraws the entry (`merge-withdrawn`); a vanished task resumes in-review, keeping its entry. A lost merge result reads as merged.

## Reaching hosts

A host does its own work through `engine host <op> --json`: one job, one JSON object on stdout, exit 0, or 1 on failure. The caller passes the model, effort and kickoff; the host reads `hosts.toml` only for its launch line, harness binary and slots. The coordinator calls its own host's ops in-process and another machine's over `ssh` (`internal/remote/ssh.go`). It refuses to run unless `~/.config/engine/host` names a machine in `hosts.toml` (`hosts-and-sessions.md`). An SSH host needs key-based SSH, `engine`, `tmux`, `gh` and the harness CLIs signed in; the coordinator and sessions use the host's own `gh` and Git configuration (`operator-setup.md`).

| Op | Does |
| --- | --- |
| `control-spawn` | Syncs engine's clone, then runs `spawn` with the run's credentials. |
| `sync-repo` | Creates or fetches engine's clone; returns the default branch. |
| `spawn` | Starts a session; the kickoff arrives on stdin (`hosts-and-sessions.md`). |
| `status` | Windows with their state and, unless working, last 20 screen lines and a waiting window's newest denial (`telemetry.md`); newest runs, each with the last 20 lines of its pane log, stripped of control sequences, when it has no live window and the log was written in the last hour; quota, free slots and signed-out harnesses (`internal/hostops/status.go`). |
| `collect` | Returns a run directory's index and file list; nothing is uploaded. |
| `merge` | Merges a PR pinned to the reviewed head, once it is open, green (no checks is green), mergeable, unblocked by branch rules and not behind its base. Per check name, the latest start (else creation) counts; undated sorts first, ties select the later rollup entry. Pending waits; failure refuses. Commit subject: PR title plus number; body: PR body. Already merged is done. |
| `release` | Closes the window and deletes each harness's files for it. For a done task, runs the repo's `dev worktree release <branch>` if provided (bounded; a failure is a note). Removes a remaining worktree unless the run failed or it has changes. Once the worktree is gone, deletes the local branch if its tip is the merged head (`--head`, from the `merged` update's request) or an ancestor of `origin`'s default branch after a fetch; otherwise keeps it and notes the commits that never reached the PR. A failed delete is a note. Any other release keeps the branch. |

**The envelope.** Every reply, a failure too, carries `protocol`, `engine` (the build) and `host`. A failure is `{"error": …, "refused": true|false}`; `refused` means the op changed nothing. Over SSH, exit 255 is an unreachable host. A caller refuses a reply on another protocol, or not an engine's, and tells the operator to run `engine setup --host <name>`.

**A failed call is unknown, never dead.** No session of a failed call or unreachable host is marked dead, failed, stalled, restarted or reaped; only a successful `status` lacking a window shows it gone.
