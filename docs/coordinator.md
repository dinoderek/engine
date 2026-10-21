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

`engine run <owner/repo#n>` is started by hand. It stays alive until the work
is done, you stop it, or the machine goes down.

| Issue | Behaviour |
| --- | --- |
| Feature (`engine:approved`) | Drives the whole feature to `done`. It plans first if there are no tasks, then runs tasks as they become ready. |
| Task | Runs only that task. It refuses to start while any blocker is open. |

**The reconcile loop.** Each pass reads state, works out what to do, and does
it. A pass can:

- spawn the planner and lint the plan;
- place and spawn ready tasks, which is `engine start` for each one;
- mark dead sessions `failed`;
- update the feature's label;
- @mention you about a stalled session;
- gather and upload telemetry for runs that have ended.

Each webhook event starts a pass: `gh webhook forward` sends repo events to a
localhost endpoint without needing a public URL. A backstop pass runs every 5
minutes. Events that arrive while a pass is pending or running coalesce into
one more pass. An event never carries state, so a missed one costs at most the
backstop interval.

Session death raises no webhook, so a session check runs every 60 seconds
between passes. It reads only the hosts, never GitHub. The first time it sees
a session waiting, it writes the time to the session's run directory as
`waiting-since`, and clears it when the session works again. It starts a pass
when a session has died or has been waiting for 10 minutes ("Session
signals").

The coordinator never claims tasks; executors claim their own. To avoid
starting a task twice in the gap between spawning it and its claim landing, it
skips any task whose tmux window already exists.

Commands that work without `run`, and come first in the build:

- `engine start <issue>`: place and launch one executor session
  (`hosts-and-sessions.md`).
- `engine ps`: every claimed task, built from the claim comments plus a
  liveness probe of each host.
- `engine attach <repo>-<task>`: attach to a session's tmux window, locally or
  over SSH.
- `engine lint <feature>`: plan lint on its own.
- `engine labels sync <owner/repo>`: create or update the `engine:` labels in
  a target repo.

The coordinator's GitHub adapter and `engine lint` implement the skill's plan
sources (`plugins/engine/skills/engine/references/plan-sources.md`) and its
lint rules (`plugins/engine/skills/engine/references/plan.md`). The skill is
the spec; the code follows it.

## Later: `engine serve`

A persistent daemon over many features, with a web UI for features, sessions
and rulings. It runs the same reconcile core, and `run` is `serve` limited to
one feature. Keep the core a package with no CLI assumptions, so `serve` only
adds to it.

## Reaching hosts

The coordinator calls `engine host <op> --json` in place for its own host
(the name in `~/.config/engine/host`), or via `ssh <address>` for any other
machine. It refuses to run unless that name is a machine in `hosts.toml`.
It reads JSON from stdout. If you are changing host names or addresses, load
`hosts-and-sessions.md`.

| Op | Does |
| --- | --- |
| `sync-repo` | Makes sure engine's own clone exists and fetches it. |
| `spawn` | Creates the worktree with the repo's command (`git worktree add` if it has none), opens the tmux window and launches the harness. |
| `status` | Reports whether the window is alive and whether the harness is waiting for input. |
| `collect` | Returns the run directory for telemetry. |
| `release` | Closes the window, and keeps the worktree when the run failed. |

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
     (`<repo>-<task>`), then by its worktree for one started by hand.
   - **Codex:** its hooks write to the run directory. Codex's `notify` is not
     used, because it holds a single program and other tools already claim it
     (Computer Use on the Mac).
3. **tmux.** If the window exists, the session is alive.

**Stalls.** A session whose `waiting-since` is 10 minutes old while its task
is not `needs-human` has stopped outside the protocol, for example on a
question asked in the session rather than on the issue. The coordinator
@mentions you on the task's issue, once per wait: it skips the mention when
its own stall comment is newer than `waiting-since`.

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
machine user. The list is hardcoded in `internal/tracker` (`TrustedUsers`),
and the newest trusted claim wins. Finding claimed tasks searches once per
trusted commenter, so it covers only issues a trusted user has commented on,
and it leaves out an issue with a claim label but no trusted claim. A claim by
anyone else is ignored, so a stranger cannot add a task. Reading the list from config is a later change.

## Salvage from `orchestrator`

Worth porting from the earlier Go repo:

- the run record schema;
- the runtime directory layout;
- the worktree manager;
- the fake-harness acceptance tests;
- its Codex sign-in check.

Its headless `codex exec` worker model does not carry over. Engine sessions are
interactive, in tmux.
