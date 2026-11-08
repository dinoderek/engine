# Server plan source and execution

Use this reference for a server planner or executor kickoff. All task state,
claims, messages and locks live in the engine server. The session's environment
supplies its endpoint, credential file and attempt ID. Ordinary gh supplies
code, PRs and CI with the operator's GitHub login.

## Read and write

- Read `engine plan show <id>`, `engine task show <id>` and
  `engine updates <id> --after <sequence>`. Follow a task's parent plan and
  owning repository too.
- Read the target repo's entrypoint and always-load docs. Repo rules win.
- Plan/task bodies are the agreed requirements/design and task card. Structured
  dependencies, capabilities, harness and tier are in the JSON fields.
- Every write supports `--request-key`. Retain it when retrying uncertain
  delivery. Updates return monotonic sequences; keep a read cursor.
- `engine task state <id> --version <current> --state <state> --pr <url>`
  changes state. Read the current version before changing it. Plans use the
  same state command.
- Use `engine tell <task> <text>` to message another task. Record rulings
  and outcomes as updates, and include them in the PR body.
- `engine ask <task> <question>` creates an explicit operator question and
  stops the task. For every hard stop from the rulings reference, ask and
  wait. Reading, acknowledgement and a conversational reply grant no approval.
  The operator resolves the question and explicitly restores task state.

## Planner

1. Read the approved plan and `engine task list <plan>`. Reuse existing
   cards on a resumed attempt.
2. Break work into one card per PR, with a final acceptance task depending
   on all the others. Create cards with `engine task create --repo <repo>
   --plan <plan> --title <text> --body-file <path>`; its body has Outcome,
   Decided, Open, Tests, Docs to update, Gates and Overrides sections.
3. Add structured dependencies with `--depends <id>,<id>`, capabilities
   with `--requires`, and optional `--harness` and `--tier`. Create cards
   before adding their dependency edges through versioned edits.
4. Every open question has one owner. Requirements and hard-stop questions
   go to the operator as updates, never into hidden assumptions.
5. Once every card is present, set the plan planned with its current version.
   Close the planner's own tmux window. The coordinator lints before dispatching.

## Executor

The server has claimed the task for this attempt and opened its worktree.

1. Read the card, parent plan, updates and repository rules. Verify all
   dependencies are done. Make decisions within scope; grade rulings with
   the rulings reference.
2. Implement with the repo's tests and docs. Read updates before making
   changes, opening a PR and entering the final merge sequence, and whenever
   the session resumes.
3. Run every required gate. Have a fresh reviewer review against the remote
   default branch, with the repo entrypoint as checklist. Fix real findings,
   rerun gates and obtain a fresh review. Record truthful harness/model
   attribution and evidence.
4. Open a normal PR with gh. Set the task in-review and record its PR URL.
   Omit GitHub issue-closing directives: the server owns task completion.
5. Take the repository merge lock with `engine lock acquire` before the
   final default-branch update and gate run. If occupied, wait and read
   updates. Hold it through review, green CI and the merge, run as a
   command of its own after any base or head check:
   `gh pr merge <n> -R <repo> --squash --match-head-commit <reviewed-sha>`.
6. Release the lock when waiting for an operator, fixing a finding, or
   closing the PR unmerged. After reacquiring it, refresh the default branch
   and repeat the final gates/review/check validation.
7. On merge, release the lock, record the outcome as a task update and mark
   the task done. Retain the worktree until the coordinator releases the
   finished session; dirty worktrees are kept.

The pre-tool merge hook verifies the lock even for pinned merges. A server
outage blocks merging; retain local output and wait. Never bypass gates or
the lock to finish during an outage.
