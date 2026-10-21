# Open questions (ephemeral)

These become tracker issues once the repo has a GitHub remote. Delete each one
when its answer lands in the owning doc.

## Decide

1. **Hook-enforced stops.** Auto mode is the default. Which hard stops also
   need a hook that blocks the action, rather than relying on the prompt? For
   example: deploy commands, a merge without `--match-head-commit`, or edits
   to gate thresholds.
2. **Pilot features.** One feature in BOGA3 and one in Gnocchi. To be decided.

## Set up

3. **Move the skills here from `agent-skills`.** The remote is
   `dinoderek/engine`. Steps, in order:
   1. Bring `plugins/task-protocol` across with its history.
   2. Make engine a plugin marketplace (`.claude-plugin/marketplace.json`).
   3. Port `install-codex.sh`.
   4. Push engine.
   5. Switch the marketplace in BOGA3's and Gnocchi's `.claude/settings.json`,
      and in BOGA3's docs, which point to the user-level skill.
   6. Re-run the Codex installer from engine, after uninstalling the old links.
   7. Archive `agent-skills`.

   Until step 5, `agent-skills` stays the live copy.
4. **Machine user.** Follow `docs/operator-setup.md`.

## Verify

5. **Codex remote from the phone.** Codex 0.163 has
   `codex remote-control start | pair` (marked experimental). Check:
   - that a phone paired this way reaches a session started in tmux;
   - that it works on the Linux host too.
6. **Existing control planes.** Before building the SSH host layer, evaluate
   Paseo, Zeron and Pragma (`prior-art.md`).
7. **Codex binary.** The Mac has two installs, the standalone one in
   `~/.local/bin` and an npm one under Homebrew. Engine should run a pinned
   path, not whichever comes first on `PATH`.
8. **Model IDs.** `gpt-6.1-sol` is confirmed (it is this Mac's Codex
   default). Check `gpt-6-luna` with a one-line `codex exec`.
9. **Reviewer tier on Codex.** Claude Code can start a subagent on a
   different model. Check whether a Codex session can, or whether its
   reviewer inherits the session's model.

## Later (low priority)

10. **Cloud spawning.** Can a script start a Claude cloud session or a Codex
    cloud task against a branch? Cloud hosts stay disabled until then.
11. **Telemetry from cloud sessions.**
