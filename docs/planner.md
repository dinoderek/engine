# Planner

> **Owns:** the planner skill: its modes, how it works with the operator, the issues it writes, and how it hands over to the coordinator. **Not here:** the phases, card fields and plan lint (`execution-protocol.md`), tiers (`execution-protocol.md`, "Models").

> **Draft.** Lines marked **Q** are open, each with a recommended answer.
> Remove the marker once it is agreed.

## Modes

The planner is one skill, `engine-plan`, with three modes. It runs in the
target repo and reads its `AGENTS.md` first.

| Mode | Who runs it | Produces |
| --- | --- | --- |
| `task` | The operator, in their own session | One task issue, ready to approve. |
| `feature` | The operator, in their own session | A feature issue in `prepare`: the requirements and the design. With `--with-tasks`, its task issues too. |
| `breakdown <feature>` | The coordinator, in a tmux session like an executor's, named `<repo>#<feature>-plan` | Task issues for an approved feature, then a passing plan lint. |

The planner always runs at the `deep` tier.

> **Q1.** Is `breakdown` the same skill as the operator-facing modes, or a
> separate one? *Recommend: the same skill.* The issue shapes and the lint
> live in one place, and `feature --with-tasks` is `breakdown` run with you
> in the session.

## Working with the operator: batches

`task` and `feature` work in batches of increasing detail, never one question
at a time.

1. **Ask.** You give the ask, at whatever level of detail you have.
2. **Next layer.** The planner reads the code and the docs, then drafts the
   whole next layer at once. It records a recommended answer for everything it
   can decide, and lists only the questions that need you.
3. **You answer in one batch,** in chat or as comments on the issue.
4. Repeat until no questions are left.

For a feature, the layers are:

| Layer | Contents |
| --- | --- |
| L1 Requirements | Objectives (numbered, each provable by a test), scope in and out, surfaces touched. |
| L2 Design | The approach, every large risk and its answer, the API, mocks for visual work, and what is left to the agent. |
| L3 Tasks | Only with `--with-tasks`: the cards and their dependencies. |

A task goes through a single layer: the card itself.

The draft lives in the issue from the first batch. Each batch overwrites the
body (`gh issue edit --body-file`) and adds a one-line comment saying what
changed, so the issue's history shows every batch. The body ends with a
**Questions** section, and it is empty when the issue is ready to approve.

> **Q2.** Should the draft go into the issue from batch one, or stay in a
> local file until it is stable? *Recommend: the issue.* The issue is the
> plan source the planner writes to, and you can read and answer from your
> phone.

## Issue shapes

### Feature

| Section | Content |
| --- | --- |
| Objectives | `O1`, `O2`, …: each one provable by a test. The acceptance task cites them. |
| Scope | In, and out. |
| Surfaces | The APIs, schemas, CLIs and screens it touches. |
| Design | The approach, and the risks with their answers. Mocks are linked. |
| Left to the agent | The open questions the executor decides as rulings. |
| Questions | For the operator. Empty when ready to approve. |

### Task card

The fields are the card fields from `execution-protocol.md`, stored as
`plan-sources.md` says for GitHub: one heading each, dependencies and the
parent as native links, and a hidden card key. The key makes `breakdown`
re-runnable (see below).

The planner fills **Gates** from the repo's lane rules and the paths the card
touches. In an engine run, the card is the operator's lane agreement
(`project-context.md`, "Quality gates"). Cards carry no size field: the
planner sizes each task to one PR on a best-effort basis.

### Kind and labels

`gh` (2.102 and later) sets a parent, "blocked by" links and an issue type
natively: `gh issue create --parent`, `--blocked-by` and `--type`. No raw API
calls are needed.

> **Q5.** Should GitHub issue types replace the `engine:feature` label?
> *Recommend: no, keep the labels as the source of truth, and also set the
> type where the owner has types.* Types are organisation-only (BOGA3's org has
> Task, Bug and Feature), while a personal repo such as engine has none.
> Labels work everywhere.

## Standalone tasks

A task with no parent feature has nothing above it to approve it, so it
carries its own approval:

- `engine:prepare` while the planner drafts it;
- `engine:approved`, set by you, after which it behaves like a `todo` task;
- `engine run <task>` refuses a standalone task without `engine:approved`.

Sub-tasks of a feature never need their own approval: the feature's approval
covers them.

### Starting from an existing issue

`engine-plan task #820` turns an issue someone has already filed into a card,
in place. The original text moves to a **Request** section at the top, and
the card follows. So there is one issue per piece of work, and whoever filed
it sees the progress. The first batch comment says the issue has become a
card.

## Breakdown

1. Read the feature issue, its comments, the repo's `AGENTS.md`, and the code
   it touches.
2. Draft every card, including the final acceptance task, which proves each
   objective and is blocked by every leaf task.
3. Set each card's tier per "Models".
4. **Write in two passes.** First, create every issue with `--parent` and its
   key, skipping any key that already exists. Then add the `--blocked-by`
   links, now that the numbers are known. A planner that dies part-way is
   simply run again.
5. Run `engine lint <feature>`, and fix and re-lint until it passes.
6. End the session. The coordinator sees the lint pass, sets the feature to
   `planned`, and starts dispatching.

`feature --with-tasks` runs steps 2 to 5 with you in the session, before
approval. Your `engine:approved` then covers the requirements, the design and
the plan together, and the coordinator skips `breakdown` because tasks exist.

## Repos with their own planning flow

BOGA3 plans as milestone and card files in `docs/plans`, written with
`task-protocol`. An executor can run those cards as they are
(`execution-protocol.md`, "Plan source"). The planner writes only to the
tracker, and the `engine:` labels mark which issues are engine's.

> **Q8.** Should the planner also write in-repo plans, and should engine
> replace `docs/plans` in BOGA3 over time? *Recommend: not yet, for either.*
> `task-protocol` already writes in-repo plans. Decide after the pilot, on
> evidence.
