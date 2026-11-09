# Execution protocol

> **Owns:** the principles, the phases at a glance, task and plan states and who moves them, the inbox, merge conflicts, and why rulings and hard stops are shaped as they are. **Not here:** what an agent does (the `build` and `engine` skills, `plugins/engine/skills/`), each harness's guards (`claude.md`, `codex.md`), dispatch and merges (`coordinator.md`), records and the inbox's storage (`control-server.md`), where sessions run (`hosts-and-sessions.md`), what a repo must provide (`project-context.md`).

Two skills hold agent behaviour. `build` takes one task to a merged PR in any
repo, outside the engine process, and holds the hard-stop classes and the
reviewer. `engine` holds the server roles (planner, executor, supervisor); its
executor is `build` with changes. This doc never restates a rule they hold.

## Principles

- **Hands-off when dispatched, stops by rule.** A dispatched executor decides
  open questions itself and stops only for a hard-stop class.
- **Enforce in code, not prompts.** The coordinator is a program: its code
  specifies a pass. Engine executors never merge; the coordinator merges the commit
  that was reviewed and gated once its checks are green. The merge guard
  (`claude.md`, "Hooks"; `codex.md`, "Guards") blocks every merge in an engine
  session and any unpinned, auto or admin merge through `gh` elsewhere. A
  guard reads the command's words and catches mistakes, not deliberate evasion.
- **One task = one session = one worktree = one PR.**
- **One plan source:** the engine server, with GitHub only for code, PRs and
  CI. For records and the inbox, load `control-server.md`.
- **Plans are never cited** in code or docs; commit messages and PR bodies may.
- **Durable decisions graduate** into the repo's owning doc, in the PR that
  ships them.

## Phases

| Phase | Who | Ends when |
| --- | --- | --- |
| 1. Prepare: requirements (the what) and design (the how) | You and a planner (`engine` skill) | You approve it (`engine plan approve`). |
| 2. Plan: tasks with dependencies and a final acceptance task | A planner, alone | Plan lint passes and the plan is `planned`. |
| 3. Execute: one task, from card to merged PR | One executor per task | The coordinator merges the PR on green. |

Prepare is the only time you are consulted, so every open question is
answered there or explicitly left to the agent. It also settles the target
repo's rules that would stop a hands-off run, such as approving merges: you
change the rule or override it for the feature. A plan's approval covers its
tasks; a standalone task carries its own.

The executor reviews its own work with a fresh reviewer at `deep`, requests
the merge, and the coordinator makes it. The supervisor finds the
coordinator's gaps: it acts only through `engine` commands and records every
gap it fills, so each becomes a coordinator fix.

Cards and roles name a tier (`deep`, `standard`, `light`), never a model. Host
config maps each tier to a model and effort per harness
(`hosts-and-sessions.md`). The planner runs at `deep` and picks each card's
tier. Telemetry records the tier and model of every run.

## Rulings and hard stops

- **You read only what is costly to undo.** One grade reaches you, `review`: a
  decision whose undo needs another PR and touches persisted data, a surface
  others use, or a shape another task builds on. A task sends its `review`
  rulings as one inbox digest with its merge request and never waits for it.
  Anything smaller is at most a one-line `low` note in the PR body.
- **Hard stops are the only interruptions, asked once.** Repeated questions
  usually share a root cause, such as one red gate on the default branch. An
  executor first looks for an answer or open question on the same root cause,
  and a question that affects every task is resolved once for the plan.
- **More than two `review` rulings on one task** mean Prepare under-specified
  the card.

## States

| Entity | States |
| --- | --- |
| Plan | `prepare` → `approved` (operator) → `planned` → `executing` → `verifying` → `done`; `blocked` while any task is `needs-human`. |
| Task | `todo` → `running` → `in-review` (PR open) → `done` by merge. `needs-human` replaces `running` or `in-review` during a hard stop; `failed` when the session dies. A standalone task starts at `prepare` → `approved`. |

"Ready" is computed, never stored. The planner creates the plan and its
tasks; you approve the plan. The executor sets `running`, `needs-human` and
`in-review`; the merge marks the task `done`. The coordinator owns the other
plan states and `failed`.

## The inbox

A task's updates in the server are the asynchronous channel to a running
session: the operator answers a hard stop or adds an instruction; one task
tells another running task when its outcome affects it (`engine tell`).
Attaching is the interactive channel.

## Merge conflicts

There is no file ownership: parallelism comes only from dependencies, and an
executor resolves conflicts by merging the default branch. Telemetry counts
conflict merges per run. Each merge moves the default branch and forces a
re-gate of every other open PR, so each repository has one merge lock
(`coordinator.md`, "Merges and merge locks"): its holder merges the default
branch, gates and requests its merge while the others wait before their last
gate run.
