# Prior art (ephemeral)

Research notes. Delete this file once the ideas worth taking have landed in the
owning docs.

| Project | Closest to | Borrowed |
| --- | --- | --- |
| [Orbi](https://github.com/orbi-build/orbi) | The whole loop: an `ai-ready` issue becomes a worktree, then a PR, then a separate review session, then a merge | GitHub Issues as the only state store. A run ID on every comment and log line. Merge only the reviewed head. A retry is a new run that keeps the old evidence. Failures classed as retryable or needing a human. |
| [Symphony](https://github.com/openai/symphony) (OpenAI) | A tracker board drives isolated autonomous runs | Ships a `SPEC.md` so any agent can build it in any language. Proof of work (CI, review feedback, walkthroughs) before landing. Manage the work, not the sessions. |
| [claude-squad](https://github.com/smtg-ai/claude-squad), dmux | A tmux session plus worktree per agent, for Claude and Codex | Confirms the session layer. We don't need a TUI: `engine ps` plus tmux is enough. |
| [crystl tmux guide](https://crystl.dev/guides/tmux-for-ai-agents/) | Mechanics for driving agents in tmux | `send-keys -l`, then a separate `Enter`. `capture-pane -J` and `pipe-pane` for logs. Its main lesson: pane scraping is brittle, so use harness hooks for state. |
| [Sortie](https://github.com/sortie-ai/sortie) | The closest full loop in Go: GitHub blocker gating, an SSH worker pool with per-host caps, Claude and Codex adapters, merge on green | Its blocker gating and escalation labels. Not a fork base: its headless sessions refuse requests for input. |
| [Paseo](https://github.com/getpaseo/paseo) | A daemon per host running Claude (Agent SDK) and Codex (app-server), with a `--json` CLI, phone apps and an E2E relay | The fallback Codex session backend. Not for Claude: SDK sessions can't use Remote Control, and subscription use through the SDK is a grey area. One maintainer, still 0.x. |
| Zeron, Pragma, diri, agent-deck | Remote control planes and session managers | Nothing to borrow: UI-first, too early, or they scrape panes. |
| [awesome-agent-orchestrators](https://github.com/andyrewlee/awesome-agent-orchestrators) | Index of the space | Check it again before building each part. |

None of them combine everything we need:

- a co-worked requirements-and-design gate;
- a dependency-graph plan with plan lint;
- rulings and hard stops;
- placement by host capability;
- both harnesses, reachable from the phone.
