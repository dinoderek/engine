# Prior art (ephemeral)

Research notes. Delete this file once the ideas worth taking have landed in the
owning docs.

| Project | Closest to | Borrowed |
| --- | --- | --- |
| [Orbi](https://github.com/orbi-build/orbi) | The whole loop: an `ai-ready` issue becomes a worktree, then a PR, then a separate review session, then a merge | GitHub Issues as the only state store. A run ID on every comment and log line. Merge only the reviewed head. A retry is a new run that keeps the old evidence. Failures classed as retryable or needing a human. |
| [Symphony](https://github.com/openai/symphony) (OpenAI) | A tracker board drives isolated autonomous runs | Ships a `SPEC.md` so any agent can build it in any language. Proof of work (CI, review feedback, walkthroughs) before landing. Manage the work, not the sessions. |
| [claude-squad](https://github.com/smtg-ai/claude-squad), dmux | A tmux session plus worktree per agent, for Claude and Codex | Confirms the session layer. We don't need a TUI: `engine ps` plus tmux is enough. |
| [crystl tmux guide](https://crystl.dev/guides/tmux-for-ai-agents/) | Mechanics for driving agents in tmux | `send-keys -l`, then a separate `Enter`. `capture-pane -J` and `pipe-pane` for logs. Its main lesson: pane scraping is brittle, so use harness hooks for state. |
| Paseo, Zeron, Pragma, diri | Remote control planes for Claude and Codex across machines | Look at these before building our own SSH host layer. |
| [awesome-agent-orchestrators](https://github.com/andyrewlee/awesome-agent-orchestrators) | Index of the space | Check it again before building each part. |

None of them combine everything we need:

- a co-worked requirements-and-design gate;
- a dependency-graph plan with plan lint;
- rulings and hard stops;
- placement by host capability;
- both harnesses, reachable from the phone.
