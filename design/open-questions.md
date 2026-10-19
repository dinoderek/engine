# Open questions (ephemeral)

These become tracker issues once the repo has a GitHub remote. Delete each one
when its answer lands in the owning doc.

## Build

1. **Coordinator language.** Go: one binary for Mac and Linux, and it can
   salvage `orchestrator`'s Codex adapter, run record and fake-harness
   acceptance tests. Or Python stdlib, matching Gnocchi. Lean: Go.
2. **Where the coordinator runs.** The always-on Linux machine, reaching the
   Mac over SSH? And is it triggered by a timer, by a GitHub webhook, or both?
3. **Executor packaging.** The executor protocol has to ship as a skill for
   both harnesses. Two choices to make:
   - Is it a hands-off mode of `task-protocol`, or a new skill?
   - Does `engine` become the marketplace, replacing `agent-skills`?

## Verify

4. **Cloud spawning.** Can a script start a Claude cloud session or a Codex
   cloud task, with a kickoff prompt, against a branch? Cloud hosts stay
   disabled until this is verified.
5. **Codex remote from the phone.** The ChatGPT app pairs through the desktop
   app. Pairing from the CLI (`codex remote-control`) is experimental. Check it
   against the installed version on both local hosts.
6. **Telemetry from cloud sessions.** What can be retrieved: transcripts?
   token counts?

## Decide

7. **Needs-human notifications.** GitHub mentions, a push service (ntfy,
   Pushover), or the harness apps' own notifications?
8. **First hard-stop list.** Is the starting list right? Which classes can the
   agent own from day one?
9. **Plan approval.** Is a look at the plan mandatory for the first features,
   then optional?
10. **Feature view.** Labels plus sub-issue progress only, or a GitHub Project
    board as well?
11. **Pilot.** Which repo and feature goes first? BOGA3 first needs its
    coverage, complexity and dependency lanes in CI before it can merge on
    green.
