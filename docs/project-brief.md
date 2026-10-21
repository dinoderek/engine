# Project brief

> **Owns:** what engine is, its current state, its scope and its stack. **Not here:** how the parts fit together (`architecture.md`), where files live (`project-structure.md`).

## What

engine is a hands-off feature build protocol for agent-driven repos, plus the
tooling that runs it. An operator and an agent agree on the *what* and the
*how*. An agent plans the work. A coordinator then runs each task in its own
agent session, on whichever host and harness fits, through to a merged PR. The
operator is out of the loop unless a task asks for them.

| Part | What it is |
| --- | --- |
| Execution protocol | The phases, roles and state machine from requirements to merged feature. |
| Coordinator | `engine`, a Go CLI for macOS and Linux. It drives a feature issue through planning and execution, spawning sessions on local or SSH hosts. No LLM. |
| Skill | `engine`: what agents do when they plan, execute (hands-off or gated) and review. It ships from this repo as a Claude Code plugin. |
| Plan sources | Plan and status storage behind one set of operations: GitHub Issues, which the coordinator drives, or an in-repo plan. |
| Hosts | Config for each machine: its harnesses, memory (which sets its session slots) and capabilities, plus how to reach every session. |
| Telemetry | Raw run data in a standard format, stored off-machine. |
| Project context guideline | What a repo must provide so executors can run hands-off. It is the source for a language-neutral seed prompt. |

## State

The parts are designed, in `docs/`. Built so far: `engine version`,
`engine labels sync`, the `engine` skill and its merge guard (a Claude Code
plugin this repo publishes), and the repo's own tooling: `./dev` and its
quality gates.
engine is the first worked example of the project context guideline's core.

## Scope

- **In:** the protocol, the coordinator CLI, the skill, the GitHub plan
  source, host config, telemetry, and the guideline with its seed prompt.
- **Out, for now:** `engine serve` (the daemon and web UI), trackers other
  than GitHub, a GitHub App identity, and deploys: engine has no deploy
  target, so `./dev` has no `deploy` verb.

## Stack

- Go, one module, version pinned in `go.mod`. The runtime uses the standard
  library only. Dev tools (golangci-lint, govulncheck) are go.mod tools.
- Talks to the outside world only through CLIs: `git`, `gh`, `tmux`, `ssh`
  and the harnesses (Claude Code, Codex).
- GitHub Actions CI, and Dependabot for version freshness.
