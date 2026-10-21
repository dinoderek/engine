# Execution protocol

> **Owns:** the principles, the phases at a glance, task and feature states and who moves them, why rulings and hard stops are shaped as they are. **Not here:** what an agent does in each mode, the card fields, the ruling grades and the hard-stop list (the `engine` skill, `plugins/engine/skills/engine/`), how the coordinator runs (`coordinator.md`), where sessions run (`hosts-and-sessions.md`), what a repo must provide (`project-context.md`).

The skill is the single source for agent behaviour. This doc explains the
design around it and never restates a rule the skill holds.

## Principles

- **Hands-off by default, stops by rule.** The executor decides open questions
  itself, and logs as rulings only the decisions worth your review. It stops
  only for a hard-stop class.
- **Enforce in code, not prompts.** The coordinator is a program. Merging is a
  scripted check of the reviewed head commit against green checks.
- **One task = one session = one worktree = one PR.**
- **One plan source per feature:** the tracker, or an in-repo plan that the
  operator points the executor at. The coordinator drives only the tracker.
- **Plans are never cited** in code or docs. Commit messages and PR bodies
  may.
- **Durable decisions graduate** into the repo's owning doc, in the PR that
  ships them.

## Phases

| Phase | Who | Skill mode | Ends when |
| --- | --- | --- | --- |
| 1. Prepare: requirements (the what) and design (the how) | You and a planner, in batches | `plan feature`, `plan task` | You label the issue `engine:approved`. |
| 2. Plan: task issues with dependencies and a final acceptance task | A planner, alone | `plan breakdown` | Plan lint passes. |
| 3. Execute: one task, from card to merged PR | One executor per task | `execute` | The PR merges on green. |

Prepare carries more weight than in a human-gated flow. It is the only time
you are consulted, so every open question is either answered there or
explicitly left to the agent. A feature's approval covers its tasks. A
standalone task carries its own.

The executor reviews its own work with a fresh reviewer (mode `review`), and
merges itself once every check is green, with `--match-head-commit` pinned to
the commit that was reviewed and gated.

## Models

Cards and roles name a tier (`deep`, `standard`, `light`), never a model.
Host config maps each tier to a model and an effort level for each harness
(`hosts-and-sessions.md`), so a model upgrade is one config change and no card
goes stale. The planner runs at `deep` and picks each card's tier. The
reviewer runs at `deep`. There is no automatic escalation: telemetry records
the tier and the model for every run, and the defaults are tuned from that.

## Rulings and hard stops

- **You read only what is costly to undo.** Decisions are graded by what
  undoing them costs. `low` ones stay in a collapsed PR section that you never
  have to read. `med` and `high` ones queue under `engine:ruling-review`.
- **Hard stops are the only interruptions.** The list grows or shrinks from
  evidence. Telemetry counts rulings per task by risk, and a class of `high`
  rulings that keeps being reversed becomes a hard stop.
- A task with several `med` or `high` rulings points to a card that Prepare
  under-specified.

## States

All labels use the `engine:` prefix.

| Entity | States |
| --- | --- |
| Feature | `prepare` → `approved` (operator) → `planned` → `executing` → `verifying` → `done`. `blocked` while any task is `needs-human`. |
| Task | `todo` → `running` ⇄ `needs-human` → `in-review` (PR open) → closed by merge. `failed` when the session dies. A standalone task starts at `prepare` → `approved`. |

"Ready" is computed, never stored. You set `approved`. The executor sets
`needs-human`, `in-review` and the `ruling-review` marker. The merge closes the
task. The coordinator owns every other transition.

## Merge conflicts

There is no file ownership. Parallelism comes only from dependencies, and an
executor resolves conflicts by merging the default branch. Telemetry counts
conflict merges per run, so this can be revisited on evidence.
