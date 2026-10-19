# engine

A hands-off feature build protocol for agent-driven repos, and the tooling
that runs it. You and an agent agree the *what* and the *how*. An agent plans
the work. Then a coordinator runs each task in its own agent session, on
whichever host and harness fits, through to a merged PR, without you in the
loop unless a task asks for you.

**Status:** design draft. The code so far is `engine version` and the repo's
own tooling: `./dev` and its quality gates.

## Parts

| Part | What it is |
| --- | --- |
| Execution protocol | The phases, roles and state machine from requirements to merged feature. |
| Coordinator | `engine`, a Go CLI for macOS and Linux. It drives a feature issue through plan and execution, spawning sessions on local or SSH hosts. No LLM. |
| Skills | The planner, executor and reviewer protocols, plus `task-protocol` for human-gated work. All ship from this repo, as a Claude Code plugin marketplace and with a Codex symlink installer. |
| Tracker adapter | Plan and status storage behind one interface. GitHub Issues first. |
| Hosts | Config for each machine or cloud: its harnesses, session cap and capabilities. Rules for how you reach every session. |
| Telemetry | Raw run data in a standard format, stored off-machine. |
| Project context guideline | What a repo must provide so executors can run hands-off. It is the source for a language-neutral seed prompt. |

## Docs

Load only what your task needs:

| If you are… | Load |
| --- | --- |
| changing phases, roles, task states, rulings, hard stops or merge rules | `docs/execution-protocol.md` |
| changing how the coordinator runs, triggers, reaches hosts or identifies itself | `docs/coordinator.md` |
| changing host config, placement or session access | `docs/hosts-and-sessions.md` |
| changing what is recorded about runs or where it is stored | `docs/telemetry.md` |
| setting up engine's GitHub identity, tokens, notifications or harness sign-in | `docs/operator-setup.md` |
| changing what a target repo must provide, or the seed prompt | `docs/project-context.md` |
| changing a gate lane, a quality target, a baseline or `./dev` | `docs/quality-gates.md` |
| changing a Go, tool or Action version, or handling a Dependabot PR | `docs/versions.md` |
| deciding something not yet settled | `design/open-questions.md` and `design/prior-art.md` (ephemeral) |
