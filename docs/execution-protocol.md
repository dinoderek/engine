# Execution protocol

> **Owns:** the principles, the phases at a glance, why the coordinator is a program, task and feature states and who moves them, the inbox, merge conflicts, why harnesses guard merges in code, and why rulings and hard stops are shaped as they are. **Not here:** what an agent does in each mode, the card fields, the ruling grades and the hard-stop list (the `build` and `engine` skills, `plugins/engine/skills/`), each harness's guards (`claude.md`, `codex.md`), how the coordinator runs (`coordinator.md`), where sessions run (`hosts-and-sessions.md`), what a repo must provide (`project-context.md`).

The skills are the single source for agent behaviour. `build` holds the
loop that takes one task to a merged PR in any repo, its hard stops and its
reviewer; `engine` holds the server roles (planner, executor, supervisor),
its executor written as `build` with changes. This doc explains the design
around them and never restates a rule they hold. If you are changing what an
agent does, change the skill.

## Principles

- **Hands-off when dispatched, stops by rule.** A dispatched executor decides
  open questions itself, and logs as rulings only the decisions worth your
  review. It stops only for a hard-stop class.
- **Enforce in code, not prompts.** The coordinator's pass is specified only
  by its code. Merging is a scripted check of the
  reviewed head commit against green checks. In Claude Code and in Codex, the
  merge guard (`claude.md`, "Hooks"; `codex.md`, "Guards") blocks every merge
  in an engine session, and an unpinned, auto or admin merge through `gh`
  elsewhere.
- **One task = one session = one worktree = one PR.**
- **One plan source:** the engine server. For server state, inbox and
  repository merge locks, load `control-server.md`.
- **Plans are never cited** in code or docs. Commit messages and PR bodies
  may.
- **Durable decisions graduate** into the repo's owning doc, in the PR that
  ships them.

## Phases

| Phase | Who | Skill mode | Ends when |
| --- | --- | --- | --- |
| 1. Prepare: requirements (the what) and design (the how) | You and a planner, in batches | `engine` `plan` | You approve it (`engine plan approve`). |
| 2. Plan: tasks with dependencies and a final acceptance task | A planner, alone | `engine` `server planner` | Plan lint passes and the plan is `planned`. |
| 3. Execute: one task, from card to merged PR | One executor per task | `engine` `server executor`, or `build` | The PR merges on green. |

Prepare carries more weight than in a human-gated flow. It is the only time
you are consulted, so every open question is either answered there or
explicitly left to the agent. A feature's approval covers its tasks. A
standalone task carries its own.

Prepare also settles the target repo's rules that would stop a hands-off run,
such as agreeing lanes or approving merges. The planner reads them in the
repo itself, and you either change the rule or override it for the feature.
Target repos need no engine-specific wording.

The executor reviews its own work with a fresh reviewer (`build`'s `review` mode). It
requests the merge and the coordinator makes it, pinned to the commit that was
reviewed and gated (`control-server.md`).

**Executing a task:**

- **Modes:** hands-off, or gated for a run you approve step by step.
- **Starters:** the coordinator, or you by hand. The steps are the same
  whichever starts it.

## Run modes

The coordinator is a program, with no agent mode: its code is the spec for
a reconcile pass. Outside engine, `build` needs no coordinator: you start
the session and, in gated mode, approve its merge.

The supervisor mode exists to find the coordinator's gaps. It acts only
through `engine` commands and records every gap it fills, so each one becomes
a coordinator fix, until a plan needs no agent watching it.

## Models

Cards and roles name a tier (`deep`, `standard`, `light`), never a model.
Host config maps each tier to a model and an effort level for each harness
(`hosts-and-sessions.md`), so a model upgrade is one config change and no card
goes stale. The planner runs at `deep` and picks each card's tier. The
reviewer runs at `deep`. There is no automatic escalation: telemetry records
the tier and the model for every run, and the defaults are tuned from that.

## Rulings and hard stops

- **You read only what is costly to undo.** One grade reaches you,
  `review`: a decision whose undo needs another PR and touches persisted
  data, a surface others use, or a shape another task builds on. Each task
  sends its `review` rulings as one inbox digest with its merge request, and
  never waits for it to be read. Anything smaller is at most a one-line `low`
  note in the PR body, or nothing.
- **Rulings are written for you, not the code.** Each says what a user would
  notice and what the alternative was.
- **Hard stops are the only interruptions, asked once.** Most repeated
  questions share a root cause, not a class: the same red gate on the default
  branch, or the same broken host sign-in. So an executor first looks for an answer or an open question on the same
  root cause, and a question that affects every task is resolved once for the
  whole plan.
- **More than two `review` rulings on one task** point to a card that
  Prepare under-specified.

## States

| Entity | States |
| --- | --- |
| Plan | `prepare` → `approved` (operator) → `planned` → `executing` → `verifying` → `done`. `blocked` while any task is `needs-human`. |
| Task | `todo` → `running` → `in-review` (PR open) → `done` by merge. `needs-human` replaces `running` or `in-review` during a hard stop. `failed` when the session dies. A standalone task starts at `prepare` → `approved`. |

"Ready" is computed, never stored. The planner creates the plan and its
tasks. You approve the plan. The executor sets `running`, `needs-human` and
`in-review`; the merge marks the task `done`. The coordinator owns the other
plan states and `failed`.

Plan status at a glance: the plan's state and its tasks' states.

## The inbox

A task's inbox is its updates in the server, the asynchronous channel to a
running session:

- **Operator to session**, from anywhere the server is reachable: answer a
  hard stop, add a small instruction, or say "stop, the design changed".
- **Session to session:** when one task's outcome changes another task that is
  already running, the first sends it a message (`engine tell`).

Attaching to the session is the interactive channel. The skill says when an
executor reads the inbox.

## Merge conflicts

There is no file ownership. Parallelism comes only from dependencies, and an
executor resolves conflicts by merging the default branch. Telemetry counts
conflict merges per run, so this can be revisited on evidence.

**One merge at a time.** The server has one atomic merge lock per repository,
covering every plan and standalone task. Each merge moves the default branch
and forces a re-gate on every other open PR, so the lock's holder merges the
default branch, gates and requests its merge, while the others wait before
their last gate run.

## Harness guards

A harness may guard the merge and permission rules in code. Each harness's
guards are in its doc (`claude.md`, `codex.md`). Every other hard stop rests on
the skill. A guard reads the command's words; it catches mistakes, not a
session set on getting round it.
