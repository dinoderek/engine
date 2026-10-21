---
name: engine
description: Engine's hands-off build protocol. Use when the operator asks you to plan work for engine (one task, or a feature, as GitHub issues), when you are started to break an approved feature into tasks, when you are told to execute a task with the engine executor protocol (a GitHub task issue or an in-repo card), or when you are spawned as an engine reviewer.
argument-hint: "plan task [<issue>] | plan feature [--with-tasks] | plan breakdown <feature> | execute <task ref> | review"
---

# Engine

Engine builds features hands-off. The operator and a planner agree the *what*
and the *how*. The work is split into tasks, and each task runs in its own
session through to a merged PR. The operator is pulled in only by a hard stop.

## Modes

| Mode | You are | Load |
| --- | --- | --- |
| `plan task [<issue>]` | Planning one task with the operator | `references/plan.md`, `references/plan-sources.md` |
| `plan feature [--with-tasks]` | Planning a feature with the operator | `references/plan.md`, `references/plan-sources.md` |
| `plan breakdown <feature>` | Splitting an approved feature into tasks, alone | `references/plan.md`, `references/plan-sources.md` |
| `execute <task ref>` | The executor of one task | `references/execute.md`, `references/rulings.md`, `references/plan-sources.md` |
| `review` | A reviewer spawned by an executor | `references/review.md` |

The kickoff prompt `Execute task <ref> with the engine executor protocol` means
`execute <ref>`.

## Rules in every mode

1. **Load the repo first.** Read the repo's `AGENTS.md` (or `CLAUDE.md`) and
   what it says to always load.
2. **The repo owns the how; engine owns the flow.** Take commands, paths,
   gates, worktree verbs and the PR template from the repo. Where the repo's
   workflow says to stop and ask the operator (to agree lanes, sign off a
   design, or approve a merge), an engine run does not ask: the card, or this
   skill, stands in for that agreement. Hard stops, including any the repo
   adds, always apply.
3. **One plan source.** A plan lives either in GitHub issues or in the repo's
   plan files, never both. Read and update state only in that source.
   `references/plan-sources.md` says where each thing lives in each source.
4. **Plans are never cited.** Code, tests and docs, other than the plan
   itself, never mention an issue number, plan path or task ID. Commit
   messages and PR bodies may.
5. **Durable decisions graduate.** Anything that must stay true after the
   work ships goes into the repo's owning doc, in the PR that ships it.
6. **One task = one session = one worktree = one PR.**

## Tiers

Cards and roles name a tier, never a model: `deep`, `standard` or `light`.
Host config turns a tier into a model and effort level, and the coordinator
starts your session on it. Never write a model name into a card.

The kickoff prompt may name a reviewer model (`Reviewer model: <id>`). Spawn
your reviewer on it if the harness lets you choose a subagent's model;
otherwise the reviewer inherits yours.
