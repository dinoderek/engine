# Open questions (ephemeral)

These become tracker issues once the repo has a GitHub remote. Delete each one
when its answer lands in the owning doc.

## Decide

1. **Hard-stop list.** Is the starting list in `execution-protocol.md` right?
   Which of these should the agent own from day one?
   - Security, when the design already covers it.
   - Contracts that are internal only.
2. **Hands-off launch modes.** Which Claude Code permission mode, and which
   Codex sandbox and approval settings? Hard stops are only prompted today.
   Which ones also need a hook that blocks the action, such as deploy commands,
   a merge without `--match-head-commit`, or edits to gate thresholds?
3. **Pilot features.** One feature in BOGA3 and one in Gnocchi. What are they?
4. **Skills packaging.** The executor, planner and reviewer skills live here.
   Codex gets them through a symlink installer, like `agent-skills` does.
   Open: does `task-protocol` stay in `agent-skills` for human-gated work?

## Set up

5. **Machine user.** Create the account, add it as a collaborator on the pilot
   repos, and decide how its token reaches sessions while commits keep the
   correct attribution.

## Verify

6. **Codex remote from the phone.** The ChatGPT app pairs through the desktop
   app. Pairing from the CLI (`codex remote-control`) is experimental. Check it
   against the installed version on both local hosts.
7. **Existing control planes.** Before building the SSH host layer, evaluate
   Paseo, Zeron and Pragma (`prior-art.md`).

## Later (low priority)

8. **Cloud spawning.** Can a script start a Claude cloud session or a Codex
   cloud task against a branch? Cloud hosts stay disabled until then.
9. **Telemetry from cloud sessions.**
