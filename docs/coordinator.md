# Coordinator

> **Owns:** the coordinator's pass: what triggers it, dispatch, recovery, merges and merge locks, how it reaches hosts, reads session signals and recovers limits. **Not here:** the records, API, credentials and inbox it reads and writes (`control-server.md`), host config and session access (`hosts-and-sessions.md`), the protocol it drives (`execution-protocol.md`).

## Shape

- **Go, one binary (`engine`)** for macOS and Linux, the same on every host.
  It runs inside `engine serve`, is no agent and never edits code.
- **Stateless.** Each pass derives everything from the server's records and
  each host's tmux sessions, `claude agents --json` and run directories.
  A restarted server picks up the sessions still live.
- **One pass** runs every 30 seconds and once at start. `internal/reconcile`
  decides from records and fresh host reads, merges first. A pass reads every
  host's `status` in parallel; the session view's observer reuses those reads
  (`control-server.md`, "Session view"). Stopping coordination leaves
  existing sessions running.

For harness scoring and host eligibility, load `hosts-and-sessions.md`,
"Placement". For changes to lint, load
`plugins/engine/skills/engine/references/plan.md`.

## Dispatch and recovery

Plans are serialized against fresh fleet capacity. A dispatch records its
attempt, host and credential before calling the host. A restart adopts
matching sessions or retries the same dispatch identity on that host. A
dispatch for a task whose earlier attempt ran is a resume: the host opens the
task's branch from `origin` (`hosts-and-sessions.md`). A new attempt first
releases the ended one's session, once a read shows its window gone. A
refused dispatch retries each pass, noticing once per attempt.

The host serializes dispatches by run ID and returns an existing run on
replay. It uses ordinary Git credentials and identity, with isolated clones
under `~/.local/share/engine/control-repos`. Credentials travel over SSH stdin
into an owner-only run file whose path the window command exports (joined
Codex sessions receive it too); hooks recover their connection from the
worktree's server-session record. Credential and lock-token files are never
collected.

A window gone from a host that answered ends its attempt `vanished` if the
task is resumable: coordination stopped after the window was last seen live,
or the window was seen live and the task has a PR or open question, unless it
resumed a vanished attempt. The task keeps its state and resumes once any
question is resolved. Otherwise attempt and task end `failed`.
`engine attempt reconcile <id> --body <evidence>` is operator-only: it
requires a successful host read with no live matching window and a recorded
run, then applies the same rule. Unknown dispatch outcomes stay reserved.

## Merges and merge locks

One lock per repository covers every plan and standalone task. Acquiring is
atomic and records the owner and a fresh token; locks never expire, and an
ended attempt keeps its lock. `engine lock release` needs the token `engine
lock acquire` keeps in the run directory, so a late release cannot free
another attempt's lock.

Executors never merge: the pre-tool hook refuses PR merges in server
sessions. Holding the lock, the executor runs `engine task merge-ready` for
the task's in-review PR. A question or `needs-human` holds the merge, so
operator approval comes first.

Each pass the coordinator answers the newest request with the `merge` op of
the task's host and that host's `gh`; the server has no GitHub credential. A
merge marks the task done with its PR and frees the lock in one transaction;
the session keeps its window 10 minutes to report. An op refusal or a missing
lock is a `merge-refused` update that wakes the executor. Pending checks wait
and are never a stall. A failed `gh` call notifies the operator and waits for
their acknowledgement.

After checking the host and PR, the operator can inspect `engine lock show
<repo-id>` and `engine lock recover` with the current attempt, its token file
and evidence; the attempt must have ended.

## Reaching hosts

A host does its own work through `engine host <op> --json`: one job, one JSON
object on stdout, exit 0, or 1 on failure. The caller decides the model,
effort and kickoff and passes them in; the host handles its own disk and
tmux, and reads `hosts.toml` only for its launch line, harness binary and
slots. The coordinator calls its own host's ops in-process and another
machine's as `ssh -o BatchMode=yes -o ConnectTimeout=10 <address> engine host
<op> --json`. It refuses to run unless the name in `~/.config/engine/host` is
a machine in `hosts.toml` (`hosts-and-sessions.md`).

| Op | Does |
| --- | --- |
| `control-spawn` | Syncs engine's isolated clone of the repo, then runs `spawn` with the run's credentials. |
| `sync-repo` | Ensures engine's clone exists, fetches it, returns the default branch. |
| `spawn` | Creates the worktree, trusts it for the harness, opens the tmux window, launches the harness with the caller's model, effort and kickoff (stdin), writes `run.json`. |
| `status` | Lists the windows of session `engine` (alive, exit code, harness, state), `claude agents --json`, each session name's newest run, quota per harness, free slots and heavy slots, the last 20 screen lines of each window not working, a waiting window's newest `permissions.jsonl` denial (`telemetry.md`), and which harnesses their own CLI reports signed out. |
| `collect` | Returns a run directory, its `run.json` and file list; nothing is uploaded. |
| `merge` | Merges a PR with the host's `gh`, pinned to the reviewed head, once open, green (no checks is green), mergeable and not behind its base. Already merged is done. |
| `release` | Closes the session's window, deletes its Codex profile, removes its worktree unless the run failed or the worktree has changes; the branch stays. |

**The envelope.** Every reply, a failure too, carries `protocol` (an integer),
`engine` (the build) and `host`. A caller refuses a reply whose `protocol` is
not its own. A failure is `{"error": …, "refused": true|false}`; `refused`
means the op changed nothing. Over SSH, exit 255 is an unreachable host. A
reply on another protocol, or not an engine's, tells the operator to run
`engine setup --host <name>`. `engine start --check --host <name>` reads the
protocol from the refusal of the undefined op `protocol`, then streams `ssh
<address> engine start --check` and keeps its exit code.

**A failed call is unknown, never dead.** When a call fails or a host is
unreachable, nothing that host would have said is known: none of its sessions
is marked dead, failed, stalled, restarted or reaped. Only a successful
`status` that lacks a window shows it gone.

Each host keeps engine's clone at `~/.local/share/engine/repos/<owner>/<repo>`.
An SSH host needs
key-based SSH, `engine`, `tmux`, `gh` and the harness CLIs signed in. The
coordinator and the host's sessions use the host's own working `gh` and Git
configuration (`operator-setup.md`).

## Session signals

Agent state is never scraped from the pane. The task's state and questions
come from the server's records. The session layer adds whether a session is
alive and waits for input:

1. **The harness** gives each window of session `engine` a `state` (`working`,
   `waiting` or `limited`) from the probe of the harness its newest `run.json`
   names (`claude.md`, "Session list"; `codex.md`, "Session signals").
   Anything else, a window with no run included, is unknown.
2. **tmux.** A window that exists with its command not exited is alive; a dead
   window is dead whatever the harness says.

**Stalls.** Limited sessions never stall. A dispatched session that has not
claimed its task 5 minutes after it started is noticed once, and one that
ended before claiming is restarted. A session that has waited 10 minutes
while its task is open and not `needs-human` has stopped outside the
protocol; the operator is notified once, naming the sign-in for a signed-out
harness. A denial at most 10 minutes older than the wait makes the stall a
Permission hard stop naming the tool, input and reason.

**Limit recovery.** Never resume `needs-human`; recheck the task's state
before typing. Each check resumes an engine window still `limited`, or
`waiting` with a positive limit `resets_at`, once the reset plus 2 minutes has
passed, by typing literal `continue` then Enter. Working, unknown and dead
sessions are left alone, as are unknown resets. The attempt is noted before
typing, so a failed Enter never duplicates text; a later reset permits
another. Waiting with a limit reset also clears the stall timer.

Still `limited` 10 minutes after the attempt, or a reset over 24 hours away
(no typing), gives one notice per reset and session. Recovery covers
executors and the plan's planner.
