---
name: engine
description: Engine's build protocol. Use when the operator asks you to plan work for engine (one task, or a feature, as GitHub issues), when you are started to break an approved feature into tasks, when you are told to execute a task with the engine executor (a GitHub task issue, owner/repo#<n>, or an in-repo card), hands-off or gated, when you are told to coordinate a feature or supervise engine run, or when you are spawned as an engine reviewer.
argument-hint: "plan task [<issue>] | plan feature [--with-tasks] | plan breakdown <feature> | execute <task ref> [hands-off | gated] | coordinate <feature> [engine | native] | supervise <feature> | review"
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
| `execute <task ref> [hands-off \| gated]` | The executor of one task | `references/execute.md`, `references/rulings.md`, `references/plan-sources.md` |
| `coordinate <feature> [engine \| native]` | The coordinator: you start executors and watch them | `references/coordinate.md`, `references/plan-sources.md` |
| `supervise <feature>` | Watching `engine run`, which coordinates | `references/supervise.md` |
| `review` | A reviewer spawned by an executor | `references/review.md` |

Kickoffs: `Execute <ref> with the engine executor, <mode> mode` means
`execute <ref> <mode>`; `Coordinate <feature> with engine` or `with native
sessions` means `coordinate`; `Supervise <feature>` means `supervise`.

## Run modes

| Run mode | Coordinator | Executors |
| --- | --- | --- |
| Single task | The operator | This session (`execute`) |
| Coordinator, native sessions | You (`coordinate native`) | Claude Code cloud sessions |
| Coordinator, engine | You (`coordinate engine`) | `engine start` |
| Supervisor | `engine run`, watched by you (`supervise`) | `engine start` |
| Engine only | `engine run` | `engine start` |

The executor's steps are the same in every run mode.

## Rules in every mode

1. **Work from the target repo.** Run in a checkout or worktree of the repo
   the work is for, and read its `AGENTS.md` (or `CLAUDE.md`) and what it
   says to always load before anything else.
2. **The repo's rules win, except where an override says otherwise.** Take
   commands, paths, gates, worktree verbs and the PR template from the repo.
   Some repo rules would stop a hands-off run, for example "agree the lanes
   with the operator" or "never merge unless asked". The planner settles each
   one before execution: the operator changes the rule, or the feature or
   card lists an **override**. Follow the overrides, and nothing else in the
   repo's rules is waived. Hard stops, including any the repo adds, always
   apply. Where the repo is silent, use `git worktree add` from the default
   branch, the repo's standard test command and a concise PR body, and say
   so.
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
