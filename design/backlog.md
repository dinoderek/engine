# Backlog (ephemeral)

Work to do before or alongside the build. Delete each item when it is done.

## Build order

1. **Engine's own context:** `AGENTS.md`, the Go module, `./dev` and its lanes,
   CI, and Dependabot. It is the first worked example of
   `docs/project-context.md`.
2. **Skills move** (alongside step 1): see "Set up" below.
3. **The `engine` skill, run by hand.** The skill, the marketplace, the
   task-issue template, `engine labels sync` and the merge guard exist. Left:
   pilot it on one BOGA3 task the operator picks (BOGA3 needs no changes: the
   planner overrides its blocking rules in the card), then on two or three
   Gnocchi task issues with at least one dependency.
4. **Plans written together.** Use `plan task` and `plan feature` on real work
   until the card and feature shapes settle.
5. **`engine start` / `ps` / `attach`.** The launcher and session overview,
   Claude only, local host.
6. **`engine run`.** The reconcile loop over a feature: dependency graph, plan
   lint, concurrency caps, feature labels. From here on, engine's own tasks can
   run through engine.
7. **Breadth.** Codex, SSH hosts (`devbox`), placement by capability, webhook
   forwarding. BOGA3 joins as a pilot.
8. **Telemetry upload.** OTel collector and R2 sync. A local `run.json` exists
   from step 5.
9. **Seed prompt and retrofit skill.**

## Set up

10. **Move `task-protocol` here from `agent-skills`**, or retire it if the
    `engine` skill's gated mode covers it by then. Engine is already a plugin
    marketplace (`plugins/engine`). Steps, in order:
    1. Bring `plugins/task-protocol` across with its history, and add it to
       `.claude-plugin/marketplace.json`.
    2. Port `install-codex.sh`, so it links every skill in `plugins/`.
    3. Switch the marketplace in BOGA3's and Gnocchi's
       `.claude/settings.json`, and in BOGA3's docs, which point to the
       user-level skill.
    4. Re-run the Codex installer from engine, after uninstalling the old
       links.
    5. Archive `agent-skills`.

    Until step 3, `agent-skills` stays the live copy of `task-protocol`.

## Check

11. **Codex remote control for a launched session.** Does a TUI started with
    `--remote` against the host's `codex remote-control` app-server show up
    in the ChatGPT app? Check on the Linux host too. It is still marked
    experimental in 0.163, and the official docs say phone setup starts in the
    desktop app (openai/codex#35928). If it fails, Codex runs under Paseo
    (`prior-art.md`) behind the same `engine host` ops, and Claude stays on
    tmux.
12. **Model IDs.** `gpt-6.1-sol` is confirmed (it is this Mac's Codex
    default). Check `gpt-6-luna` with a one-line `codex exec`.
13. **Reviewer tier on Codex.** Claude Code can start a subagent on a
    different model. Check whether a Codex session can, or whether its
    reviewer inherits the session's model.

## Parked

- **`engine host` commands.** `engine host init <name>` would write the host's
  name file and check its tools (tmux, gh, signed-in harnesses); `engine host
  add <name> --address … --ram … --capabilities …` would edit `hosts.toml`.
  Until then both files are written by hand. Revisit before `engine start`;
  the names clash with the coordinator's `engine host <op> --json` ops, so
  pick another namespace (such as `engine hosts`).
