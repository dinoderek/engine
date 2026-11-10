# Server roles

The server roles' mechanics: reading and writing the server, the planner's
writes, the executor's changes to `build`, rulings, and hard stops. The
session's environment supplies the endpoint, credential file and attempt ID
(`ENGINE_SERVER_URL`, `ENGINE_SERVER_TOKEN_FILE`, `ENGINE_ATTEMPT_ID`).

## Read and write

- `engine plan show <id>`, `engine task show <id>`, `engine task list <plan>`
  and `engine updates <id> [--after <sequence>]`. Output is JSON. Updates
  carry monotonic sequences; keep a read cursor. Follow a task to its plan
  and repository. A plan stores `state` (`prepare`, `approved`, `planned`,
  `done`); its derived `status` adds `blocked` while the operator is needed
  and `executing` once a task has started.
- A body is the agreed text: a plan's requirements and design, or a task's
  card. Dependencies, capabilities, harness and tier are JSON fields.
- `engine task state <id> --version <n> --state <state> [--pr <url>]` changes
  state; plans use `engine plan state`.
- `engine tell <id> <text>` posts a message on your task or its plan, and
  wakes the sessions waiting on it. `engine ask <id> <text>` asks the
  operator, on your task or its plan, and holds your task in `needs-human`.
  `--operator` on `tell` sends a message to the operator's inbox that asks
  for no answer.

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

- **Step 2.** Also read the parent plan, `engine updates` for the
  task and the plan, and the card's and plan's `## Overrides`. Every dependency must be
  `done`; if not, it is a **Drift** hard stop.
- **Updates are the inbox.** Read them before you change code, before the PR,
  before the merge request, and first thing when the session resumes.
- **Step 8.** Omit issue-closing directives. Put `low` notes and the
  digest in the repo template's rulings section if it has one. Then set the
  task `in-review` with its PR URL.
- **Step 10: the merge request.** You never merge, and never wait for a
  lock: the coordinator's merge queue orders merges. Once the PR is
  `in-review`, reviewed and pushed:
  1. Read updates once more. Send the rulings digest, if any (below),
     once: a repeated request does not resend it. Approval the repo or card
     requires is an `engine ask` before the request.
  2. `engine task merge-ready <task> --pr <url> --head <full-sha> --method
     <the repo's merge method> --verdict APPROVED`, then end your turn. The
     merge guard refuses `gh pr merge` in a server session. The coordinator
     waits for CI.
  3. **Wait by ending your turn**, never in a background job, polling loop
     or `sleep`: the coordinator types a pointer at its update into your window. Worked
     example: `engine task merge-ready t7 … --head 3f2c…` prints the
     request; you write "Merge requested for t7 at 3f2c…; waiting for the
     coordinator." and stop. Later, `Read the new task updates: engine
     updates t7 --after 512` arrives as your next prompt: run it, and act on
     the update it shows.
  4. The updates on the task:
     - `merged`: the task is `done`; record the outcome with `engine tell
       <task>` and stop, keeping the worktree for the coordinator to release.
     - `merge-turn`: your turn. Redo step 7 (fetch, merge the default
       branch in, rerun the gates and the review, push), then request again
       for the new head, within the time it names.
     - `merge-refused`: act on its reason (a red check, a closed PR, a
       branch rule to ask about), then request again.
     - `merge-demoted` or `merge-withdrawn`: read why; a demoted request
       stays queued.
  5. To rework the PR, set the task `running`: it leaves the queue. A resumed
     session finds its task still in-review; `engine task show <task>` shows
     its queue entry.
- **Step 11.** Leave cleanup to the coordinator.

A server outage blocks the merge request: keep your work and wait. Never
bypass gates or the queue.

## Rulings

1. **Rulings never ask.** A ruling records a decision already made; carry
   on. Only a hard stop asks and waits.
2. **One grade reaches the operator: `review`.** Only if undoing it after
   merge needs another PR *and* it touches persisted data, a surface other
   people use, or a shape another task builds on, within what the design
   covers; a contract change it does not cover is a hard stop. Anything
   else is not a ruling: the `build` skill's "Decisions" shows what is not.
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

- **Look in the server first:** `engine updates <task>` and, if the task
  has a plan, `engine updates <plan>`. A `resolved` update on either that
  decides the same root cause covers you: cite its sequence in the PR body
  and carry on. An open question on the plan already blocks your task's
  state changes and merge; if it is your root cause, end your turn and wait,
  and cite it in the PR body.
- **Ask:** `engine ask <task> <question>`, then end your turn.
- **Plan-level questions:** a gate red on the default branch, or a host tool
  or sign-in that every task on that host needs, affects every task. Ask it
  once on the plan: `engine ask <plan> <question>`. It holds your task and
  blocks every task in the plan; its resolution answers them all.
- Only the operator ends the stop. Resolving your task's last open question
  restores its state and wakes you.
