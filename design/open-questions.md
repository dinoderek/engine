# Open questions (ephemeral)

These become tracker issues once the repo has a GitHub remote. Delete each one
when its answer lands in the owning doc.

## Build order

1. **Engine's own context:** `AGENTS.md`, the Go module, `./dev` and its lanes,
   CI, and Dependabot. It is the first worked example of
   `docs/project-context.md`.
2. **Skills move** (alongside step 1): see "Set up" below.
3. **Executor skill, run by hand.** The skill, the marketplace and the
   task-issue template, `engine labels sync` and the merge guard exist. Left:
   pilot it on two or three hand-written Gnocchi task issues, with at least
   one dependency; the operator picks the feature later.
4. **Plans written together.** Write real feature plans with an agent until the
   template settles. Only then codify the planner skill.
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

## Decide

11. **Pilot features.** One feature in BOGA3 and one in Gnocchi. To be decided.

## Set up

12. **Move the skills here from `agent-skills`.** The remote is
   `dinoderek/engine`. Steps, in order:
   Engine is already a marketplace with the `engine` plugin. Left, after the
   pilot:
   1. Bring `plugins/task-protocol` across with its history, or retire it if
      the executor and a planner skill cover it by then.
   2. Port `install-codex.sh`.
   3. Switch the marketplace in BOGA3's and Gnocchi's `.claude/settings.json`,
      and in BOGA3's docs, which point to the user-level skill.
   4. Re-run the Codex installer from engine, after uninstalling the old links.
   5. Archive `agent-skills`.

   Until step 3, `agent-skills` stays the live copy of `task-protocol`.

## Verify

13. **Remote Control for a launched session.** How does `engine start` turn on
   Remote Control for an interactive `claude` session that it launches: a
   setting, a flag, or sending `/remote-control`? For Codex: does a TUI started
   with `--remote` against the host's `codex remote-control` app-server show
   up in the ChatGPT app? Check on the Linux host too.
14. **Existing control planes.** Before building the SSH host layer, evaluate
   Paseo, Zeron and Pragma (`prior-art.md`).
15. **Codex binary.** The Mac has two installs, the standalone one in
   `~/.local/bin` and an npm one under Homebrew. Engine should run a pinned
   path, not whichever comes first on `PATH`.

## Later (low priority)

16. **Cloud spawning.** Can a script start a Claude cloud session or a Codex
   cloud task against a branch? Cloud hosts stay disabled until then.
17. **Telemetry from cloud sessions.**
