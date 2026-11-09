# Backlog (ephemeral)

Open work not yet in a plan. Delete each item when it is done.

## Build

1. **Telemetry upload.** The OTel collector, the finalised `run.json` and the
   R2 sync: `telemetry.md` in this directory.
2. **Seed prompt and retrofit skill:** `seed-prompt.md` in this directory.

## Check

3. **Reviewer model in a session.** The kickoff names a reviewer model. A
   Claude executor's harness has refused a full model ID (`claude-opus-5-5`)
   as a subagent model and fell back to the `opus` alias; Codex executors'
   reviewers have run on the session's own model. Decide whether the kickoff
   should name an alias for Claude, and whether a Codex reviewer can take
   another model at all.
4. **Codex light tier.** `gpt-6-luna` is configured but has never run a task.
   Check it with a one-line `codex exec`.
5. **Claude's suggested next prompt.** An idle Claude session shows a
   suggested prompt in its input box, which reads like an operator's unsent
   draft. Disable prompt suggestions in engine sessions if the CLI allows it.
6. **Live limit recovery.** A real Claude or Codex limit stop, and the
   coordinator's automatic resume after the reset, under `engine serve`, have
   only been proved by tests and a recorded Codex failed-turn replay.
7. **Codex sessions in the ChatGPT app.** Joined Codex sessions show in the
   Codex app; the ChatGPT app has not been checked.

## Parked

- **`engine host` commands.** `engine host init <name>` would write the host's
  name file and check its tools; `engine host add <name> --address … --ram …
  --capabilities …` would edit `hosts.toml`. Until then both files are written
  by hand and `engine setup` checks them. The names clash with the
  coordinator's `engine host <op> --json` ops, so pick another namespace (such
  as `engine hosts`).
