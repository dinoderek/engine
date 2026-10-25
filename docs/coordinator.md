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

`engine run <owner/repo#n>` is started by hand, on one host, and drives Claude
sessions there through `engine start`. It stays alive until the work is done,
you stop it, or the machine goes down, and logs one timestamped line per
action. It exits 1 when the run ends short of done: a feature that is not
approved, or a lone task that failed.

| Issue | Behaviour |
| --- | --- |
| Feature (`engine:approved`) | Drives the whole feature to `done`. It plans first if there are no tasks, then runs tasks as they become ready. |
| Task | Runs only that task. It refuses to start while any blocker is open. |

**The reconcile loop.** Each pass reads state, works out what to do, and does
it. The skill's `coordinate` mode
(`plugins/engine/skills/engine/references/coordinate.md`) is the spec for a
pass, so an agent coordinator and `engine run` behave the same.

A pass runs every 5 minutes. Between passes, a session check runs every 60
seconds. It reads only the host, never GitHub, and starts a pass early when a
session has started, ended or changed between working and waiting, or has
waited long enough to stall. Designed, not built: a pass on each webhook event
(`gh webhook forward` to a localhost endpoint), coalescing events that arrive
mid-pass, and SSH hosts. The first time it sees
a session waiting, it writes the time to the session's run directory as
`waiting-since`, and clears it when the session works again ("Session
signals"). Its own notes, such as having reported a failing lint, live in
`<repo>-<n>-coordinator` beside the run directories.

The coordinator never claims tasks; executors claim their own. To avoid
starting a task twice in the gap between spawning it and its claim landing, it
skips any task whose session is still alive.

The coordinator's GitHub adapter and `engine lint` implement the skill's plan
sources (`plugins/engine/skills/engine/references/plan-sources.md`) and its
lint rules (`plugins/engine/skills/engine/references/plan.md`). The skill is
the spec; the code follows it.

## Later: `engine serve`

A persistent daemon over many features, with a web UI. It runs the same
reconcile core, which stays a package with no CLI assumptions.

## Reaching hosts

A host does its own work through `engine host <op> --json`: one job, one
JSON object on stdout, and exit 0, or 1 for a failure. The caller makes
every decision that reads the tracker, and picks the model, the effort and
the kickoff, and passes them in. The host does what touches its own disk,
tmux and token, and reads only its own entry in `hosts.toml`: its launch
line, harness binary and slots. `engine start` calls its own host's
ops in-process. Designed, not built: calling another machine's as
`ssh <address> engine host <op> --json`. The coordinator refuses to run
unless the name in `~/.config/engine/host` is a machine in `hosts.toml`. If
you are changing host names or addresses, load `hosts-and-sessions.md`.

| Op | Does |
| --- | --- |
| `sync-repo` | Makes sure engine's own clone exists, fetches it, and returns its default branch. |
| `spawn` | Creates the worktree with the repo's command (`git worktree add` if it has none), trusts it, opens the tmux window, launches the harness with the caller's model, effort and kickoff (on stdin), and writes `run.json`. |
| `status` | Lists the windows of the session `engine` (alive, exit code), `claude agents --json`, the newest run of each session name, the free slots, and the last 20 screen lines of each window whose session is not busy. |
| `collect` | Returns a run directory, its `run.json` and its file list. Nothing is uploaded. |
| `release` | Closes the session's window if it is open, and removes its worktree unless the run failed. A worktree with changes is kept too, and the branch always stays. |

**The envelope.** Every reply, a failure too, carries `protocol` (the wire
format's version, an integer), `engine` (the build) and `host` (the host's
name). A caller refuses a reply whose `protocol` is not its own. A failure
is `{"error": …, "refused": true|false}`; `refused` means the op changed
nothing, so the caller reports it as a refusal of its own.

**A failed call is unknown, never dead.** When a call fails or a host cannot
be reached, everything that host would have said is unknown: none of its
sessions is marked dead, failed or reaped for it. Only a successful
`status` that lacks a window shows the window gone.

- **Repos.** Each host keeps engine's own clone at
  `~/.local/share/engine/repos/<owner>/<repo>`, which acts as the main
  checkout for the repo's worktree command. There is no per-host path config.
- **Requirements for an SSH host:** key-based SSH, plus `engine`, `tmux`, `gh`,
  and the harness CLIs, signed in.
- The coordinator can run on any host, including one that is also running
  sessions.

## Session signals

Agent state is never scraped from the pane. The task's state comes from its
labels; the session layer adds only whether a session is alive and whether it
is waiting for input. Three sources:

1. **Tracker labels** set by the executor (`needs-human`, `in-review`).
2. **The harness**, for "waiting for input":
   - **Claude Code:** `claude agents --json` on the host lists every live
     session, including those in tmux, with its `pid`, `cwd`, `name`,
     `sessionId` and `status`. Engine matches a session by its name
     (`<repo>-<task>`), then by its worktree (its branch, in the claim's
     repo) for one started by hand. `busy` is working; `waiting` and `idle`
     are waiting for input, because only a prompt moves the session on.
   - **Codex:** its hooks write to the run directory. Codex's `notify` is not
     used, because it holds a single program and other tools already claim it
     (Computer Use on the Mac).
3. **tmux.** If the window exists and its command has not exited, the
   session is alive.

On this host, `engine ps` calls a session alive when its tmux window is live
or `claude agents` lists it. It calls one dead only when its claim names a
window of engine's session, that window is gone or its command exited,
`claude agents` does not list it and no probe failed: a session started by
hand can run anywhere, so its liveness is unknown unless listed. A failed
probe also leaves it unknown. A session in `needs-human` or `in-review` keeps
that state while alive.

**Stalls.** A window the coordinator opened whose task has not claimed in
time is stalled before the claim, for example on a prompt (the skill's
`coordinate` mode sets the time). One that ended before claiming is reported
once, then started again. A session whose `waiting-since` is 10
minutes old while its task is open and not `needs-human` has stopped outside
the protocol, for example on a question asked in the session rather than on
the issue. The coordinator @mentions you on the task's issue, once per wait:
it skips the mention when its own stall comment is newer than
`waiting-since`.

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
