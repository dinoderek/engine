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
| Coordinator | The reconcile core runs in `engine serve` or the GitHub-mode CLI, spawning sessions on local or SSH hosts. No LLM. |
| Skills | `build`: one task to a merged PR in any repo, and its reviewer. `engine`: the server planner, executor and supervisor. One Claude Code and Codex plugin ships both. |
| Control server | Plans, tasks, execution attempts, updates, operator inbox and repository merge locks. CLI access through an authenticated private API. |
| Hosts | Each machine's harnesses, session slots and capabilities, and how to reach every session. |
| Telemetry | Raw run data in a standard format, stored off-machine. |
| Project context guideline | What a repo must provide so executors can run hands-off. It is the source for a language-neutral seed prompt. |

## State

Built: the control server and its CLI, `engine version`,
`engine labels sync`, `engine start` (one Claude session on
this machine, or another one over SSH), `engine host` (its ops), `engine ps`, `engine attach`, `engine tell`, `engine lint`, `engine run` (across hosts), `engine ui` (a read-only local dashboard), the host config reader
(`internal/hosts`), the `build` and `engine` skills and their merge guard (a Claude Code
plugin this repo publishes), and the repo's own tooling: `./dev` and its
quality gates.
engine is the first worked example of the project context guideline's core.

## Scope

- **In:** the protocol, control server, coordinator, skill, GitHub compatibility,
  host config, telemetry design, and the project context guideline.
- **Out:** dashboard writes, phone push, automatic tracker migration, and deploys:
  engine has no deploy target, so `./dev` has no `deploy` verb.

## Stack

- Go, one module, version pinned in `go.mod`. The runtime uses the standard
  library only. Dev tools (golangci-lint, govulncheck) are go.mod tools.
- External tools: `git`, `gh`, `tmux`, `ssh`, `sqlite3` and the harnesses.
  Engine clients use the server's versioned HTTP API.
- GitHub Actions CI, and Dependabot for version freshness.
