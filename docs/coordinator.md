# Coordinator

> **Owns:** the coordinator's pass: what triggers it, dispatch, recovery, the merge queue, how it reaches hosts, reads session signals and recovers limits. **Not here:** the records, API, credentials and inbox it reads and writes (`control-server.md`), host config and session access (`hosts-and-sessions.md`), the protocol it drives (`execution-protocol.md`).

## Shape

- **No agent.** The coordinator, inside `engine serve`, never edits code.
- **Stateless.** Each pass derives everything from the server's records and
  each host's tmux sessions, harness observations and run directories.
  A restarted server adopts live sessions.
- **One pass** runs every 30 seconds and once at start. `internal/reconcile`
  decides from records and fresh host reads, merge queues first. A pass reads every
  host's `status` in parallel; the session view's observer reuses those reads
  (`control-server.md`, "Session view"). Stopping coordination leaves
  sessions running. Plans move only to `planned` and `done`.

For harness scoring and host eligibility, load `hosts-and-sessions.md`,
"Placement". For changes to lint, load
`plugins/engine/skills/engine/references/plan.md`.

## Dispatch and recovery

Plans are serialized against fresh fleet capacity. A dispatch records its
attempt, host and credential before calling the host. A restart adopts
matching sessions or retries the same dispatch identity on that host. A
dispatch for a task whose earlier attempt ran is a resume, on the task's
branch from `origin` (`hosts-and-sessions.md`). A new attempt first
releases the ended one's session, once a read shows its window gone. A
refused dispatch retries each pass, noticing once per attempt.

The host serializes dispatches by run ID and returns an existing run on
replay. It uses the host's Git credentials and identity, with isolated clones under
`~/.local/share/engine/control-repos`. Credentials travel over SSH stdin
into an owner-only run file whose path the window command exports; hooks recover their connection from the
worktree's server-session record. Credential files are never collected.

A window gone from a host that answered ends its attempt `vanished` if the
task is resumable: coordination stopped after the window was last seen live,
or the window was seen live and the task has a PR or open question, unless it
resumed a vanished attempt. The task keeps its state and resumes once any
question is resolved. Otherwise attempt and task end `failed`.
`engine attempt reconcile <id> --body <evidence>` is operator-only: it
requires a successful host read with no live matching window and a recorded
run, then applies the same rule. Unknown dispatch outcomes stay reserved.

## Merge queue

Each repository has one merge queue across plans and standalone tasks.
`engine task merge-ready`, for the in-review PR at its reviewed, pushed head,
queues the task; a later request keeps the place.

Passes run the queues first. The head, the first eligible entry in
request order (in-review, coordinated, no unresolved question or
unacknowledged hold), stays head until it merges, leaves or is demoted. Only
the head merges, through its task host's `merge` op; the server has
no GitHub credential. A merge marks the task done with its PR; the session
keeps its window 10 minutes to report. A done task's session is released
once idle 10 minutes; releasing one whose background job still runs is a
notice. Otherwise the op's code routes it: a
head behind, conflicting or moved gets `merge-turn`, waking the executor to
refresh (merge the default branch, gate, review, push) and request again;
red, closed or a branch rule ends the entry `merge-refused`, telling the
operator of the last two; merged elsewhere is done, and the operator told; pending waits.

Every wait is bounded in `internal/reconcile/queue.go`. A head outstaying
one (refresh, checks, mergeability, an unreachable host) goes to the back
(`merge-demoted`, with an operator notice); repeated demotions drop it.
Repeated `gh` failures hold it until the operator acknowledges;
the next entry proceeds. Waiting PRs are read periodically, catching a red,
closed or merged one early. Leaving in-review except for
`needs-human` withdraws the entry (`merge-withdrawn`); a vanished task
resumes in-review, keeping its entry. A lost merge result reads as merged.

## Reaching hosts

A host does its own work through `engine host <op> --json`: one job, one JSON
object on stdout, exit 0, or 1 on failure. The caller passes the model, effort
and kickoff; the host handles its own disk and tmux, reading `hosts.toml`
only for its launch line, harness binary and slots. The coordinator calls its own host's ops in-process and another
machine's as `ssh -o BatchMode=yes -o ConnectTimeout=10 <address> engine host
<op> --json`. It refuses to run unless the name in `~/.config/engine/host` is
a machine in `hosts.toml` (`hosts-and-sessions.md`).

| Op | Does |
| --- | --- |
| `control-spawn` | Syncs engine's isolated clone of the repo, then runs `spawn` with the run's credentials. |
| `sync-repo` | Ensures engine's clone exists, fetches it, returns the default branch. |
| `spawn` | Creates the worktree, trusts it for the harness, opens the tmux window, launches the harness with the caller's model, effort and kickoff (stdin), writes `run.json`. |
| `status` | Lists each window of session `engine` (alive, exit code, harness, state, background, the last 20 screen lines unless working, a waiting window's newest `permissions.jsonl` denial (`telemetry.md`)), each session name's newest run, quota per harness, free and heavy slots, the harnesses signed out by their own CLI, and `harness_errors`. |
| `collect` | Returns a run directory, its `run.json` and file list; nothing is uploaded. |
| `merge` | Merges a PR with the host's `gh`, pinned to the reviewed head, once open, green (no checks is green), mergeable, unblocked by branch rules and not behind its base; replies with a code. `--check` only judges. Already merged is done. |
| `release` | Closes the session's window, deletes each harness's files for it, removes its worktree unless the run failed or the worktree has changes; the branch stays. |

**The envelope.** Every reply, a failure too, carries `protocol` (an integer),
`engine` (the build) and `host`. A failure is `{"error": …, "refused":
true|false}`; `refused` means the op changed nothing. Over SSH, exit 255 is
an unreachable host. A caller refuses a reply on another protocol, or not an
engine's, telling the operator to run `engine setup --host <name>`.

**A failed call is unknown, never dead.** Nothing a failed call or
unreachable host would have said is known: none of its sessions is marked
dead, failed, stalled, restarted or reaped. Only a successful `status`
lacking a window shows it gone.

Each host keeps engine's clone at `~/.local/share/engine/repos/<owner>/<repo>`.
An SSH host needs
key-based SSH, `engine`, `tmux`, `gh` and the harness CLIs signed in. The
coordinator and sessions use the host's own `gh` and Git configuration (`operator-setup.md`).

## Session signals

Agent state is never scraped from the pane; the task's state and questions
are server records. The session layer adds whether a session is alive
and takes input:

1. **The harness** gives each window of session `engine` a `state` (`working`,
   `waiting` or `limited`) from the observation of the harness its newest
   `run.json` names (for how each harness reads it, load `harnesses.md`).
   Anything else, a window with no run included, is unknown.
2. **tmux.** A window that exists with its command not exited is alive; a dead
   window is dead whatever the harness says.

A session is **idle** when `waiting`, or `working` with `background` set:
only a background job runs, so typing cuts into no turn. Only idle sessions get wake-up pointers.

**Stalls.** Limited, queued and background-only sessions never stall. A dispatched session that has not
claimed its task 5 minutes after it started is noticed once, and one that
ended before claiming is restarted. A session waiting 10 minutes
on an open task with no hard stop or question has
stopped outside the protocol; the operator is notified once, naming the sign-in for a signed-out
harness. A denial at most 10 minutes older than the wait makes the stall a
Permission hard stop naming the tool, input and reason.

**Limit recovery.** Never resume `needs-human`; recheck the task's state
before typing. Each check types literal `continue` then Enter into a window
still `limited`, or `waiting` with a positive limit `resets_at`, once the
reset plus 2 minutes has passed; other sessions and unknown resets are left
alone. The attempt is noted first, so a failed Enter never duplicates text.
Waiting with a limit reset also clears the stall timer.

Still `limited` 10 minutes after the attempt, or a reset over 24 hours away
(no typing), gives one notice per reset and session. Recovery covers
executors and planners.
