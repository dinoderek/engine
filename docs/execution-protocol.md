# Execution protocol

> **Owns:** phases, roles, task and feature states, rulings, hard stops, merge rules. **Not here:** how the coordinator runs (`coordinator.md`), where sessions run (`hosts-and-sessions.md`), what a repo must provide (`project-context.md`).

## Principles

- **Hands-off by default, stops by rule.** The executor decides open questions
  itself and logs each decision as a ruling. It stops only for a hard-stop class.
- **Enforce in code, not prompts.** The coordinator is a program. Merging is a
  scripted check of the reviewed head commit against green checks.
- **One task = one session = one worktree = one PR.**
- **The tracker is the only state store.** The requirements, design, plan,
  task cards and their states live in the tracker, never in the repo. Code and
  docs never cite them. Commit messages and PR bodies may.
- **Durable decisions graduate.** Anything that must stay true after the
  feature ships moves into the repo's owning doc, in the PR that ships it.

## Phases

### 1. Prepare (co-work with the operator)

- **Requirements (the what).** Objectives stated so a test can prove them,
  scope in and out, the API, and mocks for visual work.
- **Design (the how).** High level. It covers every large risk and is good
  enough to plan from. Its shape varies with the feature.

Both live in the feature issue, or are linked from it. Prepare ends when the
operator labels the feature `engine:approved`. This is the only approval in the
protocol. Prepare carries more weight than in a human-gated flow: every open
question is either answered here, or explicitly left for the agent to decide.

### 2. Plan (agent alone, no approval)

The coordinator spawns a planner session for an approved feature that has no
tasks. The planner turns the requirements and design into task issues with
dependencies. It also adds a final acceptance task that proves the feature's
objectives end to end.

A task card holds:

| Field | Content |
| --- | --- |
| Outcome | What the PR makes true, provable by a test. |
| Decided | Fixed by Prepare or earlier tasks. Links to the owning doc rather than restating it. |
| Open | Questions the executor decides, each logged as a ruling. |
| Tests | The claims to prove and the layer each belongs in. |
| Docs to update | The owning docs this change touches. |
| Depends on | Blocking tasks (tracker dependencies). |
| Requires | Host capabilities such as `ios`, `docker` or `android`. |
| Harness / model | Optional. Defaults come from host config. |

**Plan lint** runs before anything is dispatched:

- the dependency graph has no cycles;
- every card has an outcome, tests and a requires list;
- every task stays within the size budget (about 2000 added lines, not
  counting generated files such as lockfiles);
- the final acceptance task exists.

If lint fails, the planner fixes the plan. Once it passes, execution starts.

### 3. Execute (one executor session per task)

The executor is one skill with two modes:

- **Hands-off:** rulings and hard stops, and it merges on green.
- **Gated:** the old `task-protocol` behaviour. It asks you about open
  questions, waits for your approval before merging, and suggests the next
  task.

The steps are the same in both modes. They work whoever started the session:
`engine start`, the coordinator, or you by hand.

**Card sources.** A task card is either a tracker issue or a card file in the
repo (`task-protocol`'s milestone and card format). The steps don't change.
Only the bookkeeping does:

| Step | Issue card | Repo card file |
| --- | --- | --- |
| Claim | Label plus claim comment | None. The session and its branch are the claim. |
| Rulings | Issue comments, plus the PR body | PR body only |
| Inbox | Issue comments | PR comments, once the PR is open |
| Hard stop | `needs-human` plus an @mention | Ask in the session |
| Close | `Closes #<task>` | The repo's plan-file rules: the PR deletes the card and marks it done in the milestone, and the last card deletes the milestone |

The coordinator only drives issue cards. Repo card files are for sessions you
start yourself.

1. **Claim.** If the task already has `engine:running`, stop and ask. Otherwise
   set `engine:running` and post the claim comment: run ID, host, harness, tmux
   target and remote-control name (`hosts-and-sessions.md`).
2. **Open.** If the session is not already in the task's worktree, create one
   with the repo's command and work there.
3. **Load and check freshness.** Read the card, the feature issue and the
   comments on both. Check the card's assumptions against the default branch.
4. **Build** with tests, and update the owning docs.
5. **Gates.** Run the repo's single gate command to green, including any lane
   that runs only locally.
6. **Review.** Spawn a fresh reviewer that did not write the diff. The verdict
   is the first line, `Verdict: APPROVED` or `Verdict: CHANGES_REQUESTED`. Fix
   every real finding, then review again. The reviewer uses the same harness by
   default; a different harness is optional per feature.
7. **Gates again.**
8. **Checkpoint.** Read the inbox.
9. **PR.** Write the body per the repo's template, with the gate evidence, the
   rulings, the deviations, and `Closes #<task>`. Set `engine:in-review`.
10. **Merge on green.** Watch the PR's checks with `gh`. On a failure, fix it
   and go back to step 5. When every check is green, merge with
   `gh pr merge --match-head-commit <sha>`, where `<sha>` is the commit that
   was reviewed and gated, so nothing unreviewed lands. If the default branch
   moved or conflicts, merge it in, then repeat from step 5. A repo with no CI
   merges on local gates alone. Branch protection is optional. Read the inbox
   before merging. In gated mode, wait for the operator's approval here.
11. **Report.** Post an outcome comment on the task's issue: what shipped, the
   rulings, and the deviations. Update downstream tasks per "Changing the plan
   mid-flight".
12. **Cleanup.** Run the repo's worktree-release command, then end the session.

## Rulings and hard stops

A ruling is one comment on the task's issue, and is repeated in a Rulings
section of the PR body. Its first line is fixed, so rulings can be searched:

```text
Ruling (risk: low|med|high): <decision>
Why: <reason>
If wrong: <what it costs to undo, what it touches>
```

**Finding rulings.** A task with any medium- or high-risk ruling gets the label
`engine:ruling-review`. The operator reads the rulings and removes the label.
So "what needs my review" is one saved search across every repo:
`is:issue label:engine:ruling-review`. Hard-stop classes are added or removed
as the rulings show.

**The agent owns** these decisions and logs them as rulings:

- internal design, naming and structure;
- which test layer a claim belongs in;
- choices the design left open;
- refactors inside the modules the task touches;
- splitting its task, or adding `todo` tasks inside the feature's scope;
- UI details within the agreed mocks;
- adding dev-only dependencies.

**Hard stops** set the task to `needs-human`, @mention the operator, and wait in
the live session:

1. **Product:** a product decision that neither the docs nor the requirements
   answer.
2. **Data:** a destructive or irreversible data change, such as a migration
   that drops or rewrites data, or a backfill.
3. **Security:** auth, permissions, row-level security, or secrets handling.
4. **Contract:** a change the design doesn't cover to a public API, schema,
   protocol or CLI surface that others use.
5. **Gates:** loosening a gate (a threshold, a suppression or a baseline entry).
6. **Outward or costly:** deploying, touching hosted environments, store
   releases, paid services, or sending messages.
7. **Drift:** the default branch has moved in a way that invalidates the card.
8. **Objectives:** the feature's requirements would have to change.
9. **Review:** two review rounds that don't converge.
10. **Environment:** a required gate that cannot run on this host.

A repo adds its own classes in `AGENTS.md`. For example, Gnocchi would add any
new runtime dependency.

## States

All labels use the `engine:` prefix.

| Entity | States |
| --- | --- |
| Feature | `prepare` → `approved` (operator) → `planned` → `executing` → `verifying` → `done`. `blocked` while any task is `needs-human`. |
| Task | `todo` → `running` ⇄ `needs-human` → `in-review` (PR open) → closed by merge. `failed` when the session dies. |

"Ready" is computed, never stored. The operator sets `approved` and clears
`ruling-review`. The executor sets `running`, `needs-human`, `in-review` and
`ruling-review`. The merge closes the task. The coordinator owns the feature
labels and `failed`.

## The inbox

A task's inbox is the comment thread on its issue. It is the asynchronous
channel to a running session:

- **Operator to session**, from anywhere GitHub works: answer a hard stop,
  add a small instruction, or say "stop, the design changed".
- **Session to session:** when one task's outcome changes another task that is
  already running, the first comments on the second's issue.

Attaching to the session is the interactive channel. Executors read the inbox
after loading, before opening the PR, before merging, and when resuming from a
hard stop.

## Changing the plan mid-flight

- An executor may edit any task that is still `todo`.
- It never edits a `running` or `needs-human` task. It writes to that task's
  inbox instead.
- A plan-level change (a new task, or one that is no longer needed) is made on
  `todo` tasks, plus a `Deviation:` comment on the feature issue.

## Merge conflicts

There is no file ownership. Parallelism comes only from dependencies, and an
executor resolves conflicts by merging the default branch. Telemetry counts
conflict merges per run, so this can be revisited on evidence.

## Tracker adapter

| Operation | GitHub mapping |
| --- | --- |
| Feature | An issue labelled `engine:feature`, with the state label. |
| Tasks | Sub-issues of the feature. |
| Dependencies | Issue dependencies ("blocked by"). |
| State | Labels. |
| Inbox, rulings, deviations | Issue comments. |
| PR link and close | `Closes #<task>` in the PR body. |

Feature status at a glance: the feature issue's state label and its sub-issue
progress. Other trackers plug in behind the same operations.
