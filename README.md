# engine

> **Owns:** what engine is, for people arriving at the repo. **Not here:** the rules and the doc index (`AGENTS.md`), the parts, state and scope (`docs/project-brief.md`).

A hands-off feature build protocol for agent-driven repos, and the tooling
that runs it. You and an agent agree the *what* and the *how*. An agent plans
the work. Then a coordinator runs each task in its own agent session, on
whichever host and harness fits, through to a merged PR, without you in the
loop unless a task asks for you.

**Status:** design draft. The code so far is `engine version` and the repo's
own tooling: `./dev` and its quality gates.

## Start here

- Agents, and anyone changing the repo: `AGENTS.md`. It holds the rules and
  routes to every doc.
- To set up a machine: `./dev doctor`, then `./dev test`.
