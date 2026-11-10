# Execution protocol

> **Owns:** the principles, the phases at a glance, task and plan states and who moves them, the inbox, merge conflicts, and why rulings and hard stops are shaped as they are. **Not here:** what an agent does (the `build` and `engine` skills, `plugins/engine/skills/`), what each harness's adapter provides (`harnesses.md`), dispatch and merges (`coordinator.md`), records and the inbox's storage (`control-server.md`), where sessions run (`hosts-and-sessions.md`), what a repo must provide (`project-context.md`).

The `build` and `engine` skills hold agent behaviour; this doc never restates
their rules.

## Principles

- **Hands-off when dispatched, stops by rule.** An executor decides open
  questions itself and stops only for a hard-stop class.
- **Enforce in code, not prompts.** Engine executors never merge; the
  coordinator merges the reviewed and gated commit once its checks are green.
  The merge guard blocks merges in an engine session and unpinned, auto or
  admin merges elsewhere, catching mistakes, not deliberate evasion.

## Phases

| Phase | Who | Ends when |
| --- | --- | --- |
| 1. Prepare: requirements (the what) and design (the how) | You and a planner | You approve it (`engine plan approve`). |
| 2. Plan: tasks with dependencies and a final acceptance task | A planner, alone | Plan lint passes and the plan is `planned`. |
| 3. Execute: one task, from card to merged PR | One executor per task | The coordinator merges the PR on green. |

Prepare is the only time you are consulted, so every open question is
answered there or left to the agent. It also settles any target repo rule
that would stop a hands-off run, such as approving merges: you change the
rule or override it for the feature. A plan's approval covers its tasks. For
watching a run, load `operator-setup.md`, "Watching a run".

Cards and roles name a tier (`deep`, `standard`, `light`), never a model;
host config maps each tier to a model and effort per harness
(`hosts-and-sessions.md`).

## Rulings and hard stops

- **You read only what is costly to undo.** One grade reaches you, `review`: a
  decision whose undo needs another PR and touches persisted data, a surface
  others use, or a shape another task builds on. A task sends its `review` rulings as
  one inbox digest with its merge request and never waits for it. Anything
  smaller is at most a `low` note in the PR body. More than two `review`
  rulings mean Prepare under-specified the card.
- **Hard stops are the only interruptions, asked once.** Repeated questions
  usually share a root cause, such as one red gate on the default branch; a
  question every task shares is asked once, on the plan.

## States

| Entity | States |
| --- | --- |
| Plan | `prepare` → `approved` (operator) → `planned` → `done`. Its status adds `blocked` while the operator is needed (a plan question, a task `needs-human` or `failed`) and `executing` once a task starts. |
| Task | `todo` → `running` → `in-review` (PR open) → `done` by merge. `needs-human` replaces `running` or `in-review` until the hard stop's last open question is resolved; `failed` when the session dies. |

"Ready" and plan status are computed (`control.PlanStatus`), never stored.
You approve the plan; the planner, or the coordinator on passing lint, sets
`planned`. The executor sets `running`, `needs-human` and `in-review`; the
merge marks the task `done`. The coordinator sets a plan `done` and a task
`failed`.

## The inbox and merge conflicts

A task's updates in the server are the asynchronous channel to a running
session: the operator answers a hard stop or adds an instruction, and a task
tells another when its outcome affects it (`engine tell`).

There is no file ownership: parallelism comes only from dependencies, and an
executor resolves conflicts by merging the default branch. Each repository has
one merge queue (`coordinator.md`, "Merge queue").
