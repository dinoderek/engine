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
| Coordinator | The reconcile core runs in `engine serve`, spawning sessions on local or SSH hosts. No LLM. |
| Skills | `build`: one task to a merged PR in any repo, and its reviewer. `engine`: the server planner and executor. One Claude Code and Codex plugin ships both. |
| Control server | Plans, tasks, execution attempts, updates, operator inbox and repository merge queues. CLI access through an authenticated private API. |
| Hosts | Each machine's harnesses, session slots and capabilities, and how to reach every session. |
| Telemetry | Each run's record, logs and harness signals, kept in a run directory on its host. |
| Project context guideline | What a repo must provide so executors can run hands-off. |

## State

Built: the control server with its one coordinator (dispatch, recovery and
merges across hosts), the `engine` CLI, the host config reader, a read-only
local dashboard, the `build` and `engine` skills with their merge guard (a
plugin this repo publishes), and the repo's own `./dev` quality gates.
engine is the first worked example of the project context guideline's core.

## Scope

- **In:** the protocol, control server and its single coordinator, the two
  skills, host config, run records, and the project context guideline.
  The control server is the only plan source; GitHub carries code, PRs and CI.
- **Out:** dashboard writes, phone push, GitHub issues or in-repo files as
  plans, and deploys: engine has no deploy target, so `./dev` has no `deploy`
  verb.

## Stack

- Go, one module, version pinned in `go.mod`. The runtime uses the standard
  library and approved libraries (`architecture.md`). Dev tools
  (golangci-lint, govulncheck) are go.mod tools.
- External tools: `git`, `gh`, `tmux`, `ssh`, `sqlite3` and the harnesses.
  Engine clients use the server's versioned HTTP API.
- GitHub Actions CI, and Dependabot for version freshness.
