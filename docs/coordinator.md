# Coordinator

> **Owns:** how the coordinator runs, what triggers it, how it reaches hosts, the identity it acts as. **Not here:** the protocol it drives (`execution-protocol.md`), host config and session access (`hosts-and-sessions.md`).

## Shape

For storage, dispatch recovery, merges and the operator inbox, load
`control-server.md`.

- **Go, one binary (`engine`)** for macOS and Linux. The same binary runs on
  every host.
- **Stateless.** Each pass derives everything from the server's records and
  from each host's tmux sessions, `claude agents --json` and run directories.
  A restarted server resumes, picking up the sessions that are still live.
- **Never edits code.** It merges a task's PR on the task's host
  (`control-server.md`).

Passes run every 30 seconds, and once at start. A pass reads every host's
`status`, parallel calls, and reads sessions from those replies.
The session view's observer reuses those reads and refreshes the stale ones
(`control-server.md`, "Session view"). Designed, not built: a pass on each
webhook event (`gh webhook forward` to a localhost endpoint).

For harness scoring and host eligibility, load `hosts-and-sessions.md`,
"Placement". For changes to lint, load
`plugins/engine/skills/engine/references/plan.md`.

## Reaching hosts

A host does its own work through `engine host <op> --json`: one job, one
JSON object on stdout, and exit 0, or 1 for a failure. The caller makes
every decision, and picks the model, the effort and the kickoff, and passes
them in. The host does what touches its own disk, and tmux, and reads
`hosts.toml` only for its own launch line, harness binary and slots. The
coordinator calls its own host's ops in-process, and another machine's as
`ssh -o BatchMode=yes -o ConnectTimeout=10 <address> engine host <op> --json`.
It reads every host's `status` for placement and its session check, reaps a
finished window, and clears an ended session before starting its task again,
with the session's host's `release`. It refuses to run unless the name in
`~/.config/engine/host` is a machine in `hosts.toml`. If you are changing
host names or addresses, load `hosts-and-sessions.md`.

| Op | Does |
| --- | --- |
| `control-spawn` | What the coordinator calls: syncs engine's isolated clone of the repo, then runs `spawn` with the run's credentials (`control-server.md`, "Dispatch and recovery"). |
| `sync-repo` | Makes sure engine's own clone exists, fetches it, and returns its default branch. |
| `spawn` | Creates the worktree with the repo's command (`git worktree add` if it has none), trusts it for the harness the caller names (Claude by default), opens the tmux window, launches the harness with the caller's model, effort and kickoff (on stdin), and writes `run.json`. |
| `status` | Lists the windows of the session `engine` (alive, exit code, harness, state), `claude agents --json`, the newest run of each session name, quota per harness (Claude’s newest run observation and a live Codex reading), free slots and free heavy slots, the last 20 screen lines of each window whose session is not working, and a waiting window's newest `permissions.jsonl` denial (`telemetry.md`). |
| `collect` | Returns a run directory, its `run.json` and its file list. Nothing is uploaded. |
| `merge` | Merges a PR with the host's `gh`, pinned to the reviewed head, once open, green (no checks is green), mergeable and not behind its base. Already merged there is done. |
| `release` | Closes the session's window if it is open, deletes its Codex profile, and removes its worktree unless the run failed. A worktree with changes is kept too, and the branch always stays. |

**The envelope.** Every reply, a failure too, carries `protocol` (the wire
format's version, an integer), `engine` (the build) and `host` (the host's
name). A caller refuses a reply whose `protocol` is not its own. A failure
is `{"error": …, "refused": true|false}`; `refused` means the op changed
nothing, so the caller reports it as a refusal of its own.

Over SSH, ssh's exit 255 is an unreachable host. A reply whose `protocol`
differs, or that is not an engine's, is an error telling the operator to run
`engine setup --host <name>`. An unknown op is refused with the envelope.
`engine start --check --host <name>` first reads the protocol from the refusal
of the op `protocol`, which no engine defines, then streams
`ssh <address> engine start --check` and keeps its exit code.

**A failed call is unknown, never dead.** When a call fails or a host cannot
be reached, everything that host would have said is unknown: none of its
sessions is marked dead, failed or reaped for it. Only a successful
`status` that lacks a window shows the window gone. The coordinator looks for
a session on the host its attempt names; while that host cannot be read, the
session is neither stalled, restarted nor reaped.

- **Repos.** Each host keeps engine's own clone at
  `~/.local/share/engine/repos/<owner>/<repo>`, which acts as the main
  checkout for the repo's worktree command. There is no per-host path config.
- **Requirements for an SSH host:** key-based SSH, plus `engine`, `tmux`, `gh`,
  and the harness CLIs, signed in.
- The coordinator can share a host with sessions.

## Session signals

Agent state is never scraped from the pane. The task's state comes from the
server's records; the session layer adds only whether a session is alive and
whether it is waiting for input. Three sources:

1. **Server records**: the task's state and open questions, set by the
   executor (`needs-human`, `in-review`).
2. **The harness**, for "waiting for input". `status` gives each window of
   session `engine` a `harness` and a `state`, `working`, `waiting` or `limited`, from
   the probe of the harness its newest `run.json` names, as each harness doc
   says (`claude.md`, "Session list"; `codex.md`, "Session signals").
   Anything else, including a window with no run, is unknown, never dead.
3. **tmux.** If the window exists and its command has not exited, the
   session is alive. A dead window is dead whatever its harness says.

The session check reads only `state`, from the `status` of the host an
attempt names, this host's too. An attempt in engine's tmux session is alive
while its window's command runs, and dead once the window is gone or its
command exited.

**Stalls.** Limited sessions never stall. A dispatched session that has not
claimed its task 5 minutes after it started is noticed once, and one that
ended before claiming is restarted. A session that has waited for 10
minutes while its task is open and not `needs-human` has stopped outside the
protocol, as on a question asked in the session. The coordinator notifies the
operator once per session start or wait.
A denial at most 10 minutes older than the wait makes the stall a Permission
hard stop, naming the tool, input and reason.

**Limit recovery.** Never resume `needs-human`; recheck the task's state
before typing. Each session check resumes an engine window still `limited`,
or `waiting` with a positive limit `resets_at`, once the reset plus 2 minutes
has passed. It types literal `continue`, then Enter; working, unknown and
dead sessions are left alone. Observed resets outlive quota expiry; unknown
resets are never resumed. The attempt is noted before typing, so Enter
failing never duplicates text. A later reset permits another attempt.
Waiting with a limit reset also clears the ordinary stall timer.

Still `limited` 10 minutes after the attempt: one notice. A reset over 24 hours away: one notice at once, and no typing. Both
alerts are once per reset and session. Recovery covers the executors and the
plan's planner.

## GitHub access

The coordinator and sessions use each host's working `gh` installation and
normal Git configuration. For host setup, load `operator-setup.md`.
