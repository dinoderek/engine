# Execution protocol

> **Owns:** phases, roles, task and feature states, rulings, hard stops, merge rules. **Not here:** where sessions run (`hosts-and-sessions.md`), what a repo must provide (`project-context.md`).

## Principles

- **Hands-off by default, stops by rule.** The executor decides open questions
  itself and logs each decision as a ruling. It stops only for a hard-stop class.
- **Enforce in code, not prompts.** The coordinator is a script. Merge-on-green
  is branch protection plus required checks. Gates run in CI.
- **One task = one session = one worktree = one PR.**
- **Prepare artifacts are ephemeral.** The PRD, design, plan and task cards
  live in the tracker, never in the repo. Code and docs never cite them.
  Commit messages and PR bodies may.
- **Durable decisions graduate.** Anything that must stay true after the
  feature ships moves into the repo's owning doc, in the PR that ships it.

## Phases

### 1. Prepare (co-work with the operator)

- **Requirements (the what).** Objectives stated so a test can prove them,
  scope in and out, the API, and mocks for visual work. Stored in the feature
  issue body. Mocks are linked.
- **Design (the how).** High level. It covers every large risk and is good
  enough to plan from. Its shape varies with the feature. Stored in the feature
  issue, or linked from it.

Prepare carries more weight than in a human-gated flow. An open question either
gets answered here, or is explicitly left for the agent to decide.

### 2. Plan (agent alone)

The planner turns the requirements and design into task issues with
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

**Plan lint** must pass before the coordinator dispatches anything:

- the dependency graph has no cycles;
- every card has an outcome, tests and a requires list;
- every task stays within the size budget (about 2000 added lines);
- the final acceptance task exists;
- no card carries a question marked `ask` (one that needs the operator).

The operator may glance at the plan, then sets the feature to `executing`.

### 3. Execute (one executor session per task)

The executor starts in the task worktree's root, so the repo's `AGENTS.md`
loads.

1. **Load and check freshness.** Read the card, the feature issue and the
   comments on both. Check the card's assumptions against the default branch.
   If drift invalidates the card, that is a hard stop.
2. **Build** with tests, and update the owning docs.
3. **Gates.** Run the repo's single gate command to green.
4. **Review.** Spawn a fresh reviewer that did not write the diff. The verdict
   is the first line, `Verdict: APPROVED` or `Verdict: CHANGES_REQUESTED`. Fix
   every real finding, then review again. Two rounds that don't converge are a
   hard stop. The reviewer uses the same harness by default; a different
   harness is optional per feature.
5. **Gates again.**
6. **Checkpoint.** Read new comments on the task's issue.
7. **PR.** Write the body per the repo's template. Add the rulings and
   deviations, and `Closes #<task>`. Enable auto-merge.
8. **Merge on green.** The executor owns its PR until it merges. If the default
   branch moved or conflicts, merge it in, re-run the gates and push. Read
   comments again before the final push.
9. **Report.** Post an outcome comment on the task's issue: what shipped, the
   rulings, and the deviations. Update downstream tasks per "Changing the plan
   mid-flight".
10. **Cleanup.** Run the repo's worktree-release command, then end the session.

### 4. Coordinate (deterministic, no LLM)

The coordinator loops on a timer and on merge events:

- **Ready:** an open task whose state is `todo` and whose blockers are all
  closed, in a feature that passed plan lint.
- **Place:** a host with a free slot, the requested harness, and
  capabilities that include everything the task requires.
- **Spawn:** create the worktree, start the session, label the task `running`,
  and comment with the host, harness, model and how to attach.
- **Watch:** if the session dies or goes silent, label the task `failed` and
  notify the operator.
- **Finish:** when the final task merges, set the feature to `verifying`. Once
  the optional audit passes, set it to `done`.

The coordinator never edits code and never merges.

## Rulings and hard stops

A ruling is one comment on the task's issue, and is repeated in the PR body:

```text
Ruling (risk: low|med|high): <decision>
Why: <reason>
If wrong: <what it costs to undo, what it touches>
```

The operator reviews rulings after the fact, riskiest first. Trust is
calibrated from evidence: early features get their rulings reviewed task by
task, and hard-stop classes are added or removed as the rulings show.

**Hard stops** set the task to `needs-human`, notify the operator, and wait in
the live session:

- a product decision that no doc answers;
- destructive data changes or migrations;
- auth, permissions or any security boundary;
- a public API or contract change that is not in the design;
- loosening a gate (a threshold, a suppression or a baseline entry);
- a card invalidated by drift;
- a review that does not converge;
- a required gate that cannot run on this host.

A repo may add classes in its `AGENTS.md`.

## States

All labels use the `engine:` prefix.

| Entity | States |
| --- | --- |
| Feature | `prepare` → `planned` → `executing` → `verifying` → `done`. `blocked` while any task is `needs-human`. |
| Task | `todo` → `running` ⇄ `needs-human` → `in-review` (PR open) → closed by merge. `failed` when the session dies. |

"Ready" is computed, never stored. The coordinator owns every transition
except three: the executor sets `needs-human` and `in-review`, and the merge
closes the task.

## Changing the plan mid-flight

- An executor may edit any task that is still `todo`.
- It never edits a `running` or `needs-human` task. It comments on that task
  instead, and the comment thread is that task's inbox.
- Executors read their inbox after loading, before opening the PR, and before
  the final push.
- A plan-level change (a new task, or one that is no longer needed) is made on
  `todo` tasks, plus a `Deviation:` comment on the feature issue. If it changes
  the feature's objectives, that is a hard stop.

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
progress, optionally on a GitHub Project board grouped by state. Other trackers
plug in behind the same operations.
