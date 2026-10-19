# engine

A hands-off feature build protocol for agent-driven repos, and the tooling
that runs it. You and an agent agree the *what* and the *how*. An agent plans
the work. Then a coordinator runs each task in its own agent session, on
whichever host and harness fits, through to a merged PR, without you in the
loop unless a task asks for you.

**Status:** design draft. Nothing is built yet.

## Parts

| Part | What it is |
| --- | --- |
| Execution protocol | The phases, roles and state machine from PRD to merged feature. |
| Coordinator | A deterministic CLI. It finds ready tasks, places them on a host, spawns an executor session and records the run. No LLM. |
| Tracker adapter | Plan and status storage behind one interface. GitHub Issues first. |
| Hosts | Config for each machine or cloud: its harnesses, session cap and capabilities. Rules for how you reach every session. |
| Telemetry | Raw run data in a standard format, stored off-machine. |
| Project context guideline | What a repo must provide so executors can run hands-off. It is the source for a language-neutral seed prompt. |

## Docs

Load only what your task needs:

| If you are… | Load |
| --- | --- |
| changing phases, roles, task states, rulings or merge rules | `docs/execution-protocol.md` |
| changing host config, placement or session access | `docs/hosts-and-sessions.md` |
| changing what is recorded about runs or where it is stored | `docs/telemetry.md` |
| changing what a target repo must provide, or the seed prompt | `docs/project-context.md` |
| deciding something not yet settled | `design/open-questions.md` (ephemeral) |
