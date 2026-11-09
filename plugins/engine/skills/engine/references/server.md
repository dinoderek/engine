# Server roles

The server roles' mechanics: reading and writing the server, the planner's
writes, the executor's changes to `build`, rulings, and hard stops. The
session's environment supplies the endpoint, credential file and attempt ID
(`ENGINE_SERVER_URL`, `ENGINE_SERVER_TOKEN_FILE`, `ENGINE_ATTEMPT_ID`).

## Read and write

- `engine plan show <id>`, `engine task show <id>`, `engine task list <plan>`
  and `engine updates <id> [--after <sequence>]`. Output is JSON. Updates
  carry monotonic sequences; keep a read cursor. Follow a task to its plan
  and repository.
- A body is the agreed text: a plan's requirements and design, or a task's
  card. Dependencies, capabilities, harness and tier are JSON fields.
- `engine task state <id> --version <n> --state <state> [--pr <url>]` changes
  state; plans use `engine plan state`.
- `engine tell <id> <text>` posts a message on your task or its plan, and
  wakes the sessions waiting on it. `engine ask <task> <text>` asks the
  operator and moves the task to `needs-human`. `--operator` on `tell` sends
  a message to the operator's inbox that asks for no answer.

## Planner

Follow `plan.md`, "Breakdown". The coordinator started you on an approved
plan; on a resumed attempt, reuse the cards `engine task list <plan>`
already shows.

- **Two passes.** Create every card with `engine task create --repo <repo>
  --plan <plan> --title <text> --body-file <path> [--requires <cap>,…]
  [--harness <name>] [--tier <tier>]`. Then, with every ID known, add the
  dependencies and fill owner card IDs into **Decided**: `engine task edit
  <task> --version <n> --depends <id>,<id>`. An edit replaces every field,
  so resend the title, body file, requires, harness and tier.
- **Ask** a blocking question with `engine ask <plan> <text>` and wait.
- **Finish.** When `engine lint <plan>` passes, run `engine plan state
  <plan> --version <n> --state planned`. Then end the session: inside tmux
  (`$TMUX_PANE` set), close your window with `cd "$HOME" && tmux
  kill-window -t "$TMUX_PANE"`.

## Executor

Follow the `build` skill, hands-off, with these changes. They replace the
repo's own merge and cleanup steps; the merge guard enforces it.

- **Step 1.** The server claimed the task and opened its worktree: work
  there, and create none.

- **Step 2.** Also read the parent plan, `engine updates` for the task and
  the plan, and the card's and plan's `## Overrides`. Every dependency must be
  `done`; if not, it is a **Drift** hard stop.
- **Updates are the inbox.** Read them before you change code, before the PR,
  before the merge request, and first thing when the session resumes.
- **Step 8.** Omit issue-closing directives. Put `low` notes and the
  digest in the repo template's rulings section if it has one. Then set the
  task `in-review` with its PR URL.
- **Step 10: the lock and the merge request.** You never merge. Once the PR
  is `in-review` and CI is green:
  1. `engine lock acquire` takes the repository's merge lock. If it is held,
     try again every 5 minutes, reading updates between tries: nothing wakes
     you when it frees.
  2. Holding it, redo step 7: fetch; if the default branch moved, merge it
     in, rerun the gates and the review, push, and wait for CI green on the
     new head.
  3. Read updates once more. Send the rulings digest, if any (below).
  4. `engine task merge-ready <task> --pr <url> --head <full-sha> --method
     <the repo's merge method> --verdict APPROVED`, then end your turn. The
     merge guard refuses `gh pr merge` in a server session. Approval the repo
     or card requires is an `engine ask` before the request.
  5. The coordinator wakes you with an update. `merged`: the task is `done`
     and the lock freed; record the outcome with `engine tell <task>` and
     stop, keeping the worktree for the coordinator to release.
     `merge-refused`: act on its reason, then request again. Keep the lock
     for a clean default-branch merge; release it (`engine lock release`)
     while you fix anything else. Any other merge notice: wait.
  6. Release the lock while you wait for the operator or close the PR
     unmerged. After taking it again, redo item 2.
- **Step 11.** Leave cleanup to the coordinator.

A server outage blocks the merge request: keep your work and wait. Never
bypass gates or the lock.

## Rulings

1. **Rulings never ask.** A ruling records a decision already made; carry
   on. Only a hard stop asks and waits.
2. **One grade reaches the operator: `review`.** Only if undoing it after
   merge needs another PR *and* it touches persisted data, a surface other
   people use, or a shape another task builds on. Anything else is not a
   ruling: the `build` skill's "Decisions" shows what is not.
3. **`low` notes** follow `build`: optional, at most five, PR body only.
4. **One digest per task.** Send every `review` ruling at once, just before
   the merge request: `engine tell <task> --operator <digest>`. Do not wait
   for it to be read. Repeat it in the PR body. Each ruling:

   ```text
   Ruling (risk: review): <decision>
   User notices: <what a user would see, in product terms, or nothing>
   Alternative: <the option you did not take>
   If wrong: <what undoing it costs and touches>
   ```

5. **Many rulings mean a weak card.** With more than two, the digest's first
   line says so, as a signal for Prepare.

## Hard stops

The classes and "Ask once" are `build`'s (`hard-stops.md`). Here:

- **Look in the server first:** `engine updates <task>` and `engine updates
  <plan>`. A plan `ruling` update, or a `resolved` one on your task or plan, that
  decides the same root cause covers you: cite its sequence with
  `engine tell <task>` and in the PR body, and carry on. A plan-level
  question still open on the same root cause: cite its sequence with
  `engine tell <task>`, end your turn, and wait to be woken by its ruling;
  record both sequences in the PR body.
  The coordinator may raise a stall notice meanwhile; that is expected.
- **Ask:** `engine ask <task> <question>`, then end your turn.
- **Plan-level questions:** a gate red on the default branch, or a host tool
  or sign-in that every task on that host needs, affects every task. Ask on
  your task, saying the question is plan-level and asking the operator to
  resolve it with `--plan <plan>`. Then, with the sequence the ask returned,
  post `engine tell <plan> "Plan-level question <sequence>: <root cause>"`
  so sibling tasks find it instead of asking again.
- Only the operator's resolution and their restoring the task's state end
  the stop; a plan resolution answers it for every task in the plan.
