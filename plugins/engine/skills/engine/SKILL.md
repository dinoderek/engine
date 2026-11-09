---
name: engine
description: Engine's server roles. Use when the operator asks you to plan work for engine as a server plan or task (Prepare), when you are started as a server planner (`server planner p<n>`) or server executor (`server executor t<n>`, or "Use the engine skill's server executor mode for t<n>"). For one task outside engine, use the `build` skill.
argument-hint: "plan <ask> | server planner <plan> | server executor <task>"
---

# Engine

Engine builds a plan hands-off. The operator and a planner agree the *what*
and the *how* (Prepare). A planner splits the approved plan into tasks. The
coordinator inside `engine serve` runs each task in its own session through
to a merged PR. The operator is pulled in only by a hard stop.

Every plan, task, claim, message, ruling and merge lock lives in the engine
server, read and written with the `engine` CLI. GitHub holds only code, PRs
and CI. Never record plan state in issues, labels or repo files.

## Modes

| Mode | You are | Load |
| --- | --- | --- |
| `plan <ask>` | Preparing a plan, or one standalone task, with the operator | `references/plan.md` |
| `server planner <plan>` | Breaking an approved plan into tasks, alone | `references/plan.md`, `references/server.md` |
| `server executor <task>` | The executor of one task | the `build` skill (`../build/SKILL.md`, `../build/references/hard-stops.md`), then `references/server.md` |

The coordinator's kickoff reads "Use the engine skill's server `<role>`
mode for `<id>`" and names the "server plan-source reference": that is
`references/server.md`. A reviewer an executor spawns uses the `build`
skill's `review` mode.

## Rules in every mode

1. **Work from the target repo.** Read its `AGENTS.md` (or `CLAUDE.md`) and
   what it says to always load before anything else. The repo's rules win,
   except where an override the operator agreed says otherwise.
2. **Plans are never cited.** Code, tests and docs never mention a plan or
   task ID. Commit messages and PR bodies may.
3. **One task = one session = one worktree = one PR.**
4. **Server writes are versioned.** Read an item's current `version` before
   an edit or state change. Every write takes `--request-key <key>`; reuse
   the key when retrying an uncertain delivery.
5. **GitHub is the host's `gh`,** with its configured sign-in.

## Tiers

Cards name a tier, never a model: `deep`, `standard` or `light`. Host config
turns a tier into a model and effort level, and the coordinator starts each
session on it. Planners run `deep`. Never write a model name into a card.
