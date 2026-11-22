# Server roles

The server roles' mechanics. The session's environment supplies the endpoint,
credential file and attempt ID (`ENGINE_SERVER_URL`,
`ENGINE_SERVER_TOKEN_FILE`, `ENGINE_ATTEMPT_ID`).

## Read and write

- `engine plan show <id>`, `engine task show <id>`, `engine task list <plan>`
  and `engine updates <id> [--after <sequence>]`. Updates
  carry monotonic sequences; keep a read cursor. Follow a task to its plan and
  repo.
- A body is the agreed text: a plan's requirements and design, or a task's
  card. Dependencies, capabilities, harness and tier are JSON fields.
- `engine task state <id> --version <n> --state <state> [--pr <url>]` changes
  state; plans use `engine plan state`.
- `engine tell <id> <text>` posts a message on your task or its plan, and
  wakes the sessions waiting on it. `engine ask <id> <text>` asks the operator,
  on your task or its plan, and holds your task in `needs-human`.
  `tell --operator` sends the operator's inbox a message that needs no answer.

## Planner

Follow `plan.md`, "Breakdown". On a resumed attempt, reuse the cards
`engine task list <plan>` shows.

- **Two passes.** Create every card with `engine task create --repo <repo>
  --plan <plan> --title <text> --body-file <path> [--requires <cap>,…]
  [--harness <name>] [--tier <tier>]`. Then, with every ID known, set
  dependencies and owner card IDs in **Decided** with `engine task edit <task>
  --version <n> --depends <id>,<id>`, which replaces every field: resend the
  title, body file, requires, harness and tier.
- **Ask** a blocking question with `engine ask <plan> <text>` and wait.
- **Finish.** When `engine lint <plan>` passes, run `engine plan state
  <plan> --version <n> --state planned`. Then end the session: inside tmux
  (`$TMUX_PANE`), close your window with `cd "$HOME" && tmux
  kill-window -t "$TMUX_PANE"`.

## Executor

Follow the `build` skill, hands-off, with these changes replacing its merge step.

- **Step 1.** The server opened the task's worktree: work there, create none.
- **Step 2.** Also read the parent plan, `engine updates` for the task and the
  plan, and the card's and plan's `## Overrides`. A dependency not `done` is a
  **Drift** hard stop.
- **Updates are the inbox.** Read them before you change code, the PR, the
  merge request, and when the session resumes.
- **Step 8.** Omit issue-closing directives. Put `low` notes (`build`,
  "Decisions") and the digest in the template's rulings section if it has one.
  Then set the task `in-review` with its PR URL.
- **Reviewer verdicts.** Post every reviewer verdict, APPROVED or not, with
  `engine tell <task>`, and list each round in the PR body.
- **Step 10: the merge request.** The coordinator's merge queue orders merges;
  you never merge or wait for a lock. Once the PR is `in-review`, reviewed and
  pushed:
  1. Send the rulings digest, if any (below), once. Approval the repo or card requires: `engine ask` before the request.
  2. Request the merge only when `gh pr checks` shows nothing pending, and
     never edit the PR body after `merge-ready`: it becomes the merge commit
     body. `engine task merge-ready <task> --pr <url> --head <full-sha>
     --method <the repo's merge method> --verdict APPROVED`, then end your
     turn. The merge guard refuses `gh pr merge`.
  3. **Wait by ending your turn**, never in a background job, polling loop or
     `sleep`. The coordinator types a pointer at its update into your window,
     as `Read the new task updates: engine updates t7 --after 512`: run it, and
     act on the update it shows.
  4. The updates on the task:
     - `merged`: the task is `done`. Record the outcome with `engine tell
       <task>`; from the repo's main checkout, tear down what you provisioned
       by its post-merge cleanup rules, **except your worktree and branch,
       which the release removes**, and do nothing else; run `engine task
       cleaned <task> [--left <what remains>]`; end your turn.
     - `merge-turn`: your turn. Redo step 7 (fetch, merge the default branch
       in, rerun the gates and the review, push), then request again for the
       new head within the time it names.
     - `merge-refused`: the entry has ended. Act on its reason (a red check, a
       closed PR, a timeout, a branch rule to ask the operator about), then
       request again. For a host or `gh` failure the operator is told: end your
       turn, and request again once they say it is fixed.
     - `merge-withdrawn`: read why.
  5. To rework the PR, set the task `running`, which leaves the queue.
- **Step 11.** On `merged`, above.

A server outage blocks the merge request: keep your work and wait. Never
bypass gates or the queue.

## Rulings

1. **Rulings never ask.** A ruling records a decision already made. Only a hard stop asks and waits.
2. **One grade reaches the operator: `review`.** Only if undoing it after
   merge needs another PR *and* it touches persisted data, a surface other
   people use, or a shape another task builds on, within what the design
   covers; a contract change it does not cover is a hard stop. Anything else
   is not a ruling (`build`, "Decisions").
3. **One digest per task.** Send every `review` ruling at once, just before the merge request, without waiting for a read: `engine tell <task>
   --operator <digest>`. Repeat it in the PR body. Each ruling:

   ```text
   Ruling (risk: review): <decision>
   User notices: <what a user would see, in product terms, or nothing>
   Alternative: <the option you did not take>
   If wrong: <what undoing it costs and touches>
   ```

4. **Many rulings mean a weak card.** With more than two, the digest's first
   line says so: a weak card, for Prepare.

## Hard stops

The classes and "Ask once" are `build`'s (`hard-stops.md`). Here:

- **Look in the server first:** `engine updates` on the task and its plan. A `resolved` update deciding the same root cause covers
  you: cite its sequence in the PR body and carry on. An open plan question
  holds only new dispatches and its asker, not you; if it is your root cause, `engine ask <task>`, citing its sequence in the question and PR body.
- **Ask on your own task by default:** `engine ask <task> <question>`, then end
  your turn.
- **A plan question needs a proved-red default branch:** ask it once with
  `engine ask <plan> <question>`. It holds your task and the plan's new
  dispatches; its resolution answers them all.
- **Host problems are the coordinator's readiness job, not a question.** A tool
  that breaks mid-task is `engine ask <task>`, never on the plan.
- **Never say the operator agreed without citing the update that holds it.**
  When the operator answers in the pane, the hook records it; do not re-post it.
- Only the operator ends the stop; resolving its last open question restores your task's state and wakes you.
