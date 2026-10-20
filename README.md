# engine

> **Owns:** what engine is, for people arriving at the repo. **Not here:** the rules and the doc index (`AGENTS.md`), the parts, state and scope (`docs/project-brief.md`).

A hands-off feature build protocol for agent-driven repos, and the tooling
that runs it. You and an agent agree the *what* and the *how*. An agent plans
the work. Then a coordinator runs each task in its own agent session, on
whichever host and harness fits, through to a merged PR, without you in the
loop unless a task asks for you.

**Status:** mostly designed, partly built: `docs/project-brief.md` ("State")
says what exists.

## Start here

- Agents, and anyone changing the repo: `AGENTS.md`. It holds the rules and
  routes to every doc.
- To set up a machine: `./dev doctor`, then `./dev test`.
- To run the executor skill in another repo with Claude Code:
  `/plugin install engine --marketplace dinoderek/engine`.
