# Coordinator

> **Owns:** how the coordinator runs, what triggers it, how it reaches hosts, the identity it acts as. **Not here:** the protocol it drives (`execution-protocol.md`), host config and session access (`hosts-and-sessions.md`).

## Shape

- **Go, one binary (`engine`)** for macOS and Linux. The same binary runs on
  every host.
- **Stateless.** Each pass derives everything from the tracker (issues, labels,
  comments, PRs) and from each host's tmux sessions, `claude agents --json`
  and run directories. Run
  the same command again and it resumes, picking up the sessions that are
  still live.
- **Never edits code and never merges.** Executors do both.

## V1: `engine run`

`engine run <owner/repo#n>` drives harness
sessions through `engine start`, on whichever host placement picks
(`hosts-and-sessions.md`, "Placement"). It stays alive until the work is done,
you stop it, or the machine goes down, and logs one timestamped line per
action. It exits 1 when the run ends short of done: a feature that is not
approved, or a lone task that failed.

| Issue | Behaviour |
| --- | --- |
| Feature (`engine:approved`) | Drives the whole feature to `done`. It plans first if there are no tasks, then runs tasks as they become ready. |
| Task | Runs only that task. It refuses to start while any blocker is open. |

`--harness-ratio claude=4,codex=1` replaces the config ratio for this run.
For harness scoring and host eligibility, load `hosts-and-sessions.md`,
"Placement".

**The reconcile loop.** Each pass reads state, works out what to do, and does
it. The skill's `coordinate` mode
(`plugins/engine/skills/engine/references/coordinate.md`) is the spec for a
pass, so an agent coordinator and `engine run` behave the same.

Passes run every 5 minutes. Between passes, a session check runs every 60
seconds. It reads every host's `status`, parallel calls, and
never GitHub, and starts a pass early when a session has started, ended,
become known or unknown, changed state or limit reset, or has
waited long enough to stall or resume from a limit. A pass reads sessions and fleet quota from those replies, keeping the newest observation per harness; expired windows read 0%. Designed, not built: a pass on each webhook event
(`gh webhook forward` to a localhost endpoint), and coalescing events that
arrive mid-pass.

The coordinator's notes live in `<repo>-<n>-coordinator` beside its own
host's run directories, whichever host a session runs on: the host it last
started each session on, `waiting-since` (written the first time it sees a
session waiting, cleared when the session works again; "Session signals"),
`limit-reset-<spawned>` (observed reset, retained until working),
`resumed-for=<resets_at>` (attempt time),
`limit-far=<resets_at>` and `limit-stuck=<resets_at>` (alert times),
the stall it last reported, and having reported a failing lint or a task no
host can take. The first note written makes the directory. A coordinator
restarted on another host has none of them, so its stall timers start
again. Resume and limit-alert notes survive restarts on the same host; notes
older than a session start are ignored.

At done, `engine run` posts the summary and closes the feature (a lone task
has neither), then re-reads the work and every host's sessions each minute.
It releases each hands-off window of a closed task as soon as the session is
waiting, with no 10-minute wait, and exits when none is left or after 15
minutes. Gated windows are never closed.

The coordinator never claims tasks; executors claim their own. To avoid
starting a task twice in the gap between spawning it and its claim landing, it
skips any task whose session is still alive, on any host that answers.

For changes to GitHub plan storage or lint, load the skill's
`plugins/engine/skills/engine/references/plan-sources.md` or
`plugins/engine/skills/engine/references/plan.md`.

## Later: `engine serve`

A persistent daemon over many features, with a web UI. It runs the same
reconcile core, which stays a package with no CLI assumptions.

## Reaching hosts

A host does its own work through `engine host <op> --json`: one job, one
JSON object on stdout, and exit 0, or 1 for a failure. The caller makes
every decision that reads the tracker, and picks the model, the effort and
the kickoff, and passes them in. The host does what touches its own disk,
tmux and token, and reads `hosts.toml` only for its own launch line,
harness binary and slots. `engine start` calls its own host's
ops in-process, and another machine's as `ssh -o BatchMode=yes -o
ConnectTimeout=10 <address> engine host <op> --json` (`engine start --host`,
and `status` for `engine ps`). `engine tell` has no op: it types the pointer
with `tmux send-keys` over the same ssh options, and exits 1, keeping its
comment, when the window is missing or the host is unreachable. `engine run`
reads every host's `status` for placement, and starts a session elsewhere as
`engine start --host` does. Its session check reads the same `status`
replies. It reaps a finished window, and clears an ended session before
starting its task again, with the session's host's `release`. The
coordinator refuses to run
unless the name in `~/.config/engine/host` is a machine in `hosts.toml`. If
you are changing host names or addresses, load `hosts-and-sessions.md`.

| Op | Does |
| --- | --- |
| `sync-repo` | Makes sure engine's own clone exists, fetches it, and returns its default branch. |
| `spawn` | Creates the worktree with the repo's command (`git worktree add` if it has none), trusts it for the harness the caller names (Claude by default), opens the tmux window, launches the harness with the caller's model, effort and kickoff (on stdin), and writes `run.json`. |
| `status` | Lists the windows of the session `engine` (alive, exit code, harness, state), `claude agents --json`, the newest run of each session name, quota per harness (Claude’s newest run observation and a live Codex reading), free slots and free heavy slots, and the last 20 screen lines of each window whose session is not working. |
| `collect` | Returns a run directory, its `run.json` and its file list. Nothing is uploaded. |
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
`status` that lacks a window shows the window gone. `engine run` looks for a
session on the host its claim names, else the host it last started it on;
while that host cannot be read, the session is neither stalled, restarted
nor reaped, and the run logs the outage once, then once when the host answers
again.

- **Repos.** Each host keeps engine's own clone at
  `~/.local/share/engine/repos/<owner>/<repo>`, which acts as the main
  checkout for the repo's worktree command. There is no per-host path config.
- **Requirements for an SSH host:** key-based SSH, plus `engine`, `tmux`, `gh`,
  and the harness CLIs, signed in.
- The coordinator can share a host with sessions.

## Session signals

Agent state is never scraped from the pane. The task's state comes from its
labels; the session layer adds only whether a session is alive and whether it
is waiting for input. Three sources:

1. **Tracker labels** set by the executor (`needs-human`, `in-review`).
2. **The harness**, for "waiting for input". `status` gives each window of
   session `engine` a `harness` and a `state`, `working`, `waiting` or `limited`, from
   the probe of the harness its newest `run.json` names, as each harness doc
   says (`claude.md`, "Session list"; `codex.md`, "Session signals").
   Anything else, including a window with no run, is unknown, never dead.
3. **tmux.** If the window exists and its command has not exited, the
   session is alive. A dead window is dead whatever its harness says.

`engine ps` and `engine run`'s session check read only `state`, from the
`status` of the host a claim names, this host's too. A claim in engine's tmux
session is alive while its window's command runs, and dead once the window
is gone or its command exited. A session started by hand is alive only when
the reply's harness list shows it (`claude.md`, "Session list"); otherwise
its liveness is unknown. A failed call leaves it unknown too. A session in
`needs-human` or `in-review` keeps that state while alive.

**Stalls.** Limited sessions never stall, including before claiming. Their
`waiting-since` is cleared; a later ordinary wait starts a fresh timer.
Claude limit markers carry `resets_at`; busy wins over a marker.
A window the coordinator opened whose task has not claimed in
time is stalled before the claim, for example on a prompt (the skill's
`coordinate` mode sets the time). One that ended before claiming is reported
once, then started again. A session whose `waiting-since` is 10
minutes old while its task is open and not `needs-human` has stopped outside
the protocol, for example on a question asked in the session rather than on
the issue. The coordinator @mentions you on the task's issue, with the last
screen the host's `status` sent (it sends one for a window whose session is
not working), once per session start or wait: it notes the start or wait it
flagged, and flags only another one.

**Limit recovery.** Never resume `needs-human`; recheck labels before typing.
Each session check resumes an engine window still `limited`,
or `waiting` with a positive limit `resets_at`, once the reset plus 2 minutes
has passed. It types literal `continue`, then Enter, as `engine tell` does,
Working, unknown and dead sessions are left alone.
Observed resets persist across quota expiry for waiting or limited windows.
Unknown resets are not resumed.
The attempt is noted before typing, preventing duplicate text even if Enter
fails, and logged with its reset. A later reset permits another attempt.
Waiting with a limit reset also clears the ordinary stall timer.

Still `limited` 10 minutes after the attempt: one @mention with the last
screen. A reset more than 24 hours away: one @mention immediately and no
typing while it remains that far away. Both alerts are once per reset and
session. Recovery covers
unclaimed engine windows and the feature’s planner.

## Identity and notifications

GitHub never notifies you about your own activity. If engine used your token,
its comments and @mentions would never reach your phone. So engine acts as its
own identity:

- **V1:** a machine user, added as a collaborator. GitHub allows one free
  machine account per person. The coordinator and every session it starts
  get its token as `GH_TOKEN`. A session started by hand runs `gh` as
  `engine gh …`, which reads the token file, so no harness needs configuring
  and the token never appears on a command line. If you are setting it up, load
  `operator-setup.md`.
- **Later:** a GitHub App.

You subscribe to (or are assigned) each feature issue. Hard stops @mention
you. GitHub mobile pushes both.

**Who engine trusts.** Repos can be public, so anyone can comment on a task.
Engine acts on a claim comment only when its author is the operator or the
machine user. The newest trusted claim wins, and a claim by anyone else is
ignored, so a stranger cannot add a task.
