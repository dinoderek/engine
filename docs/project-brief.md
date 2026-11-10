# Project brief

> **Owns:** what engine is, its current state, its scope and its stack. **Not here:** how the parts fit together (`architecture.md`), where files live (`project-structure.md`).

## What

engine is a hands-off feature build protocol for agent-driven repos, plus the
tooling that runs it. An operator and an agent agree the *what* and the
*how*, an agent plans the work, and a coordinator runs each task in its own
session through to a merged PR. The operator is pulled in only when a task
asks.

| Part | What it is |
| --- | --- |
| Execution protocol | The phases, roles and state machine from requirements to merged feature. |
| Coordinator | The reconcile core in `engine serve`, spawning sessions on local or SSH hosts. No LLM. |
| Skills | `build` (one task to a merged PR) and `engine` (server planner and executor), as one Claude Code and Codex plugin. |
| Control server | Plans, tasks, attempts, updates, operator inbox and merge queues, behind a private API. |
| Hosts, telemetry | Each machine's harnesses, slots and capabilities; each run's record and logs. |
| Project context guideline | What a repo must provide so executors can run hands-off. |

## State

Built: every part above, with a read-only local dashboard, plus the repo's
own `./dev` gates. engine is the first worked example of the project context
guideline's core.

## Scope

The control server is the only plan source; GitHub carries code, PRs and CI.
Out of scope: dashboard writes, phone push, GitHub issues or in-repo files as
plans, and deploys: engine has no deploy target, so `./dev` has no `deploy`
verb.

## Stack

Go, one module, version pinned in `go.mod`; the runtime uses the standard
library and approved libraries (`architecture.md`). External tools: `git`,
`gh`, `tmux`, `ssh`, `sqlite3` and the harnesses.
