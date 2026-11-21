# Harnesses

> **Owns:** the harness adapter contract, and what the core does with an adapter's answers. **Not here:** the `[harness.<name>]` launch lines and tiers an operator writes (`launch-lines.md`), each harness's quirks (the adapter packages' doc comments under `internal/harness/`), host config and placement (`hosts-and-sessions.md`), installing and signing in (`operator-setup.md`).

## The contract

The core knows a harness only through `internal/harness`'s `Adapter`; its
doc comments are the contract. What each answer drives:

| An adapter provides | The core |
| --- | --- |
| a session's launch: config, trust, flags, kickoff | runs the host's line with them; `release` asks every adapter to delete its per-session files |
| whether a session joins the remote-control daemon | joins only when allowed; a down daemon never refuses a spawn |
| one observation per `status`: quota, signed out, window state | unknown is never dead; a window's own exit decides that; a wait at a dialog is never typed into |
| optionally, a run's usage limit marker | a window that died with it set ends resumable (`session-signals.md`) |
| hook events in engine's terms, its denial, its unattended policy | runs the core hooks: `pre-tool-use` is the merge guard, `permission` applies the policy, `state` writes the run's state file, `prompt` records the operator's pane input |
| the prompts in a session's transcript, marking each turn the harness injected | lets the prompt hook check a pane input was submitted there, and drop one only the harness injected |
| its own hook entries | resolves `engine hook <entry> --harness <name>`; an unknown name or entry exits 1 and blocks nothing |
| `engine setup` steps, preflight failure advice, the user's plugin update | runs the steps after the host's, on hosts that list the harness; `engine upgrade` runs the update on the caller first |
| the preflight kickoff, and whether state comes from hooks alone | fails a check whose window stayed open, skipped a step, or wrote no state file |

What each harness answers today:

| | Claude Code | Codex |
| --- | --- | --- |
| unattended permission request | denied, naming the Permission hard stop | left to Codex's auto-review, which never prompts |
| session state | its own session list; a run still missing from it a minute after launch is held at a startup dialog | the hooks' state file; a missing file is unknown |
| a limit's reset | the hook's, the quota reading's, else the stop message's `resets <time>`, the next such time in the host's zone | the state file's, else the quota reading's |
| a limit marker | the run's `limit.json` | none |

**Adding a harness:** a package under `internal/harness/` that passes
`harnesstest`'s contract suite, a registry line, its `scripts/import-rules.tsv`
row, and its name in the `harness-names` lane. No other package names it.

For the launch lines and tiers an operator writes in `hosts.toml`, load
`launch-lines.md`; for the prompt hook and the typed-text log, `pane-input.md`.
