# Execution protocol

> **Owns:** the principles, the phases at a glance, task and feature states and who moves them, the inbox, merge conflicts, what the merge guard enforces, and why rulings and hard stops are shaped as they are. **Not here:** what an agent does in each mode, the card fields, the claim comment, the ruling grades and the hard-stop list (the `engine` skill, `plugins/engine/skills/engine/`), how the coordinator runs (`coordinator.md`), where sessions run (`hosts-and-sessions.md`), what a repo must provide (`project-context.md`).

The skill is the single source for agent behaviour. This doc explains the
design around it and never restates a rule the skill holds. If you are
changing what an agent does, change the skill.

## Principles

- **Hands-off when dispatched, stops by rule.** A dispatched executor decides
  open questions itself, and logs as rulings only the decisions worth your
  review. It stops only for a hard-stop class.
- **Enforce in code, not prompts.** The coordinator is a program. Merging is a
  scripted check of the reviewed head commit against green checks. In Claude
  Code, the plugin's merge guard (below) blocks an unpinned, auto or admin
  merge through `gh`.
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

Prepare also settles the target repo's rules that would stop a hands-off run,
such as agreeing lanes or approving merges. The planner reads them in the
repo itself, and you either change the rule or override it for the feature.
Target repos need no engine-specific wording.

A task issue uses the template `.github/ISSUE_TEMPLATE/engine-task.md`, one
section per card field. Dependencies have no section: they are the issue's
"blocked by" list.

The executor reviews its own work with a fresh reviewer (mode `review`), and
merges itself once every check is green, with `--match-head-commit` pinned to
the commit that was reviewed and gated.

**Executing a task:**

- **Modes:** hands-off, or gated for a run you approve step by step.
- **Starters:** `engine start`, the coordinator, or you by hand. The steps
  are the same whichever starts it.

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
  You read them and remove the label, so "what needs my review" is one saved
  search across every repo: `is:issue label:engine:ruling-review`.
- **Hard stops are the only interruptions.** The list grows or shrinks from
  evidence. Telemetry counts rulings per task by risk, and a class of `high`
  rulings that keeps being reversed becomes a hard stop.
- **Several `med` or `high` rulings on one task** point to a card that Prepare
  under-specified.

## States

All labels use the `engine:` prefix. `engine labels sync <owner/repo>`
creates or updates them all; the set is `internal/tracker`.

| Entity | States |
| --- | --- |
| Feature | `prepare` → `approved` (operator) → `planned` → `executing` → `verifying` → `done`. `blocked` while any task is `needs-human`. |
| Task | `todo` → `running` → `in-review` (PR open) → closed by merge, with no state label. `needs-human` replaces `running` or `in-review` during a hard stop. `failed` when the session dies. `todo` is the absence of a state label, never a label. A standalone task starts at `prepare` → `approved`. |

"Ready" is computed, never stored. The planner sets `feature` and `prepare`.
You set `approved` and clear `ruling-review`. The executor sets `running`,
`needs-human`, `in-review` and `ruling-review`, and clears `in-review` once
the merge closes the task.
The coordinator owns the other feature labels and `failed`.

Feature status at a glance: the feature issue's state label and its sub-issue
progress.

## The inbox

A task's inbox is the comment thread on its issue. It is the asynchronous
channel to a running session:

- **Operator to session**, from anywhere GitHub works: answer a hard stop,
  add a small instruction, or say "stop, the design changed".
- **Session to session:** when one task's outcome changes another task that is
  already running, the first comments on the second's issue.

Attaching to the session is the interactive channel. The skill says when an
executor reads the inbox.

## Merge conflicts

There is no file ownership. Parallelism comes only from dependencies, and an
executor resolves conflicts by merging the default branch. Telemetry counts
conflict merges per run, so this can be revisited on evidence.

## Enforced by hook

The plugin ships one Claude Code `PreToolUse` hook,
`engine hook pre-tool-use`. It blocks a Bash call that:

- runs `gh pr merge` without `--match-head-commit`, or with `--auto` or
  `--admin`;
- merges through `gh api`, REST or GraphQL.

`engine gh …` counts as `gh` for both.

Every other hard stop rests on the skill. A guard reads the command's words;
it catches mistakes, not a session set on getting round it. Codex sessions
have no guard yet.
