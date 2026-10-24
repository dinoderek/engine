# Coordinate a feature

You drive one approved feature to `done` by starting executors and watching
them. `engine run` runs this same pass as code. You never edit code, merge,
claim a task, or answer a hard stop: hard stops go to the operator.

| Kickoff | Executors start through |
| --- | --- |
| `Coordinate <feature> with engine` | `engine start`, read back with `engine ps --json` |
| `Coordinate <feature> with native sessions` | Claude Code cloud sessions (below) |

With neither named, use `engine` if `engine ps` runs here, else ask.

## The pass

Run a pass, then wait about 5 minutes, and repeat until the feature is
`done`. Each pass, in order:

1. **Read state.** The feature's labels and comments; each sub-issue's
   state, labels and blockers; every live session (`engine ps --json`, or
   each cloud session's status); and the sessions you started, with when.
2. **Plan if needed.** An `engine:approved` feature with no sub-issues gets a
   planner session, kickoff `Plan breakdown <feature>`, unless one you
   started for it is still alive. When the feature is `engine:approved`,
   has sub-issues and no planner is alive, lint the plan (`engine lint
   <feature>`, else by hand with `plan.md`'s lint list). If it passes, set
   `engine:planned`; if not, comment the failures on the feature and
   @mention the operator, once. Steps 3 to 8 run only on a feature that is
   `engine:planned` or later.
3. **Start ready tasks.** A task is ready when it is open, has no `engine:`
   state label, and every blocker is closed. While a slot is free, start
   them in this order:
   1. most downstream tasks first: the count of open tasks blocked by it,
      directly or through others;
   2. then `deep` before `standard` before `light`;
   3. then the lowest issue number.

   Skip a task whose session you started and that has not claimed yet,
   while that session is alive. If it died before claiming, the task is
   ready again.
4. **Stalls.** A session is stalled when:
   - it has not claimed its task 5 minutes after you started it; or
   - it has been waiting for input for 10 minutes and its task is open and
     not `engine:needs-human`.

   @mention the operator on the task's issue with the stall and the session's
   last 20 screen lines (`tmux capture-pane -p -S -20`, or the cloud session's
   last message). Once per wait: skip it if your stall comment is newer than
   the wait.
5. **Feature label.** Once a task has started, the feature's label is the
   first that holds: `engine:blocked` while any task is
   `engine:needs-human`; `engine:verifying` while only the acceptance task
   is open; else `engine:executing`. Change it only when it differs.
6. **Dead sessions.** An open task with a claim state label (`running`,
   `needs-human`, `in-review`) whose session is dead gets `engine:failed` in
   place of that label, loses `engine:merging` if it holds the lock, and gets
   a comment saying so. Its worktree stays.
7. **Reap.** A hands-off executor closes its own window after its report.
   Close a window, or archive a cloud session, you started in hands-off
   mode only when its task is closed, carries no `engine:` state label, and
   the session has been waiting for 10 minutes. Leave a gated run's window
   for the operator. Say what you closed in the pass's log line.
8. **Done.** When the feature has sub-issues and every one is closed, comment on the feature: each
   task with its PR, the acceptance result, the tasks still carrying
   `engine:ruling-review`, and the deviations. Set `engine:done`, close the
   feature as completed, and end the session.

Log one line per pass in the session: what you started, flagged, marked and
closed. Move the feature's labels only as above. Executors own the task
labels, except `engine:failed` and clearing a dead holder's `engine:merging`.

## Native sessions

With `native sessions`, each executor is a Claude Code cloud session, so no
host, tmux or `hosts.toml` is involved.

- **Start** with the harness's create-session tool: the target repo as its
  source, permission mode `dontAsk`, title `<repo>-<task>`, environment
  variable `ENGINE_UNATTENDED=1`, and the kickoff `Execute <ref> with the
  engine executor, hands-off mode. host=cloud run=<repo>-<task>-<UTC
  yyyymmddThhmmssZ> started=coordinator`.
- **The environment** must give the session `GH_TOKEN` and an `engine` on
  `PATH`, for the merge guard. Without them the executor stops
  (**Environment**).
- **Slots:** 3 live sessions at once, unless the operator says otherwise. A
  session whose task has closed holds no slot.
- **Liveness:** the session's status. A failed or archived session is dead,
  and a blocked one is waiting.
- **Answers:** when the operator answers on the task's issue, message the
  session pointing at the comment.
