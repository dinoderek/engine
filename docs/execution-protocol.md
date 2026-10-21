# Execution protocol

> **Owns:** phases, roles, task and feature states, the inbox, merge conflicts. **Not here:** what an executor does, its rulings and hard stops (the executor skill), how the coordinator runs (`coordinator.md`), where sessions run (`hosts-and-sessions.md`), what a repo must provide (`project-context.md`).

## Principles

- **Hands-off when dispatched, stops by rule.** A dispatched executor decides
  open questions itself and logs each decision as a ruling. It stops only for
  a hard-stop class.
- **Enforce in code, not prompts.** The coordinator is a program. Merging is a
  scripted check of the reviewed head commit against green checks. In Claude
  Code, the executor plugin's merge guard (below) blocks an unpinned, auto or
  admin merge through `gh`.
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
| Requires | Host capabilities such as `ios`, `linux` or `vm` (`hosts-and-sessions.md`). |
| Harness / model | Optional. Defaults come from host config. |

A task issue uses the template `.github/ISSUE_TEMPLATE/engine-task.md`, which
has one section per field. "Depends on" has no section: it is the issue's
"blocked by" list.

**Plan lint** runs before anything is dispatched:

- the dependency graph has no cycles;
- every card has an outcome, tests and a requires list;
- every task stays within the size budget (about 2000 added lines, not
  counting generated files such as lockfiles);
- the final acceptance task exists.

If lint fails, the planner fixes the plan. Once it passes, execution starts.

### 3. Execute (one executor session per task)

Each task runs in one session of the executor skill,
`plugins/engine/skills/executor/SKILL.md`. It owns the executor's steps, its
two modes, the claim comment, the ruling format, the decisions the agent owns
and the hard-stop classes. If you are changing any of those, change the skill.

- **Modes.** Hands-off, or gated for a human-approved run.
- **Card sources.** A tracker issue, or a card file in the repo for sessions
  you start yourself. The coordinator drives issue cards only.
- **Starters.** `engine start`, the coordinator, or you by hand. The steps
  don't change.

**Finding rulings.** The operator reads the rulings on each task labelled
`engine:ruling-review` and removes the label. So "what needs my review" is one
saved search across every repo: `is:issue label:engine:ruling-review`.
Hard-stop classes are added or removed as the rulings show.

## States

All labels use the `engine:` prefix.

| Entity | States |
| --- | --- |
| Feature | `prepare` → `approved` (operator) → `planned` → `executing` → `verifying` → `done`. `blocked` while any task is `needs-human`. |
| Task | `todo` → `running` → `in-review` (PR open) → closed by merge. `needs-human` replaces `running` or `in-review` during a hard stop. `failed` when the session dies. `todo` is the absence of a state label, never a label. |

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

Attaching to the session is the interactive channel. The skill says when an
executor reads the inbox.

## Merge conflicts

There is no file ownership. Parallelism comes only from dependencies, and an
executor resolves conflicts by merging the default branch. Telemetry counts
conflict merges per run, so this can be revisited on evidence.

## Enforced by hook

The executor plugin ships one Claude Code `PreToolUse` hook,
`engine hook pre-tool-use`. It blocks a Bash call that:

- runs `gh pr merge` without `--match-head-commit`, or with `--auto` or
  `--admin`;
- merges through `gh api`, REST or GraphQL.

Every other hard stop rests on the skill. A guard reads the command's words;
it catches mistakes, not a session set on getting round it. Codex sessions
have no guard yet.

## Tracker adapter

| Operation | GitHub mapping |
| --- | --- |
| Feature | An issue labelled `engine:feature`, with the state label. |
| Tasks | Sub-issues of the feature. |
| Dependencies | Issue dependencies ("blocked by"). |
| State | Labels. `engine labels sync <owner/repo>` creates or updates them all; the set is `internal/tracker`. |
| Inbox, rulings, deviations | Issue comments. |
| PR link and close | `Closes #<task>` in the PR body. |

Feature status at a glance: the feature issue's state label and its sub-issue
progress. Other trackers plug in behind the same operations.
