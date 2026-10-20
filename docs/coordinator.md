# Coordinator

> **Owns:** how the coordinator runs, what triggers it, how it reaches hosts, the identity it acts as. **Not here:** the protocol it drives (`execution-protocol.md`), host config and session access (`hosts-and-sessions.md`).

## Shape

- **Go, one binary (`engine`)** for macOS and Linux. The same binary runs on
  every host.
- **Stateless.** Each pass derives everything from the tracker (issues, labels,
  comments, PRs) and from each host's tmux sessions and run directories. Run
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
- gather and upload telemetry for runs that have ended.

A pass runs every 60 seconds. Webhooks make passes start sooner:
`gh webhook forward` sends repo events to a localhost endpoint without needing
a public URL. An event only triggers an early pass and never carries state, so
a missed event costs at most one poll interval.

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

## Later: `engine serve`

A persistent daemon over many features, with a web UI for features, sessions
and rulings. It runs the same reconcile core, and `run` is `serve` limited to
one feature. Keep the core a package with no CLI assumptions, so `serve` only
adds to it.

## Reaching hosts

A host is either `local` or `ssh`. The coordinator calls
`engine host <op> --json` in place for a local host, or via `ssh <address>` for an
SSH host. It reads JSON from stdout.

| Op | Does |
| --- | --- |
| `sync-repo` | Makes sure engine's own clone exists and fetches it. |
| `spawn` | Creates the worktree with the repo's command, opens the tmux window and launches the harness. |
| `status` | Reports whether the window is alive and the latest signal from the harness hooks. |
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

Agent state is never scraped from the pane. Three sources:

1. **Tracker labels** set by the executor (`needs-human`, `in-review`).
2. **Harness hooks** that write to the run directory: Claude Code's
   `Notification` and `Stop` hooks, and Codex's hooks. They mark "waiting for
   input" and "turn ended". Codex's `notify` is not used, because it holds a
   single program and other tools already claim it (Computer Use on the Mac).
3. **tmux.** If the window exists, the session is alive.

## Identity and notifications

GitHub never notifies you about your own activity. If engine used your token,
its comments and @mentions would never reach your phone. So engine acts as its
own identity:

- **V1:** a machine user, added as a collaborator. GitHub allows one free
  machine account per person. The coordinator and every session get its
  token as `GH_TOKEN`. If you are setting it up, load
  `operator-setup.md`.
- **Later:** a GitHub App.

You subscribe to (or are assigned) each feature issue. Hard stops @mention
you. GitHub mobile pushes both.

## Salvage from `orchestrator`

Worth porting from the earlier Go repo:

- the run record schema;
- the runtime directory layout;
- the worktree manager;
- the fake-harness acceptance tests;
- its Codex sign-in check.

Its headless `codex exec` worker model does not carry over. Engine sessions are
interactive, in tmux.
