# Backlog (ephemeral)

Work to do before or alongside the build. Design questions are marked **Q**
inline in the doc that owns them. Delete each item here when it is done.

## Set up

1. **Pilot task.** One task in BOGA3, chosen by the operator. Before it
   runs, BOGA3 needs:
   - a line in `AGENTS.md` saying that in an engine run the card's Gates field
     is the operator's lane agreement, and the executor merges on green;
   - for an in-repo card, an engine binding in `docs/plans/README.md`:
     `## Objective` → Outcome, `## Decided` → Decided, `## Open — resolve with
     the user at session start` → Open, `## Deliverables and acceptance` →
     Tests, `## Specs to update` → Docs to update, `## Gates` → Gates, and the
     milestone's `## Task breakdown` table → Depends on and state.
2. **Move `task-protocol` here from `agent-skills`.** Engine is already a
   plugin marketplace (`plugins/engine`). Steps, in order:
   1. Bring `plugins/task-protocol` across with its history, and add it to
      `.claude-plugin/marketplace.json`.
   2. Port `install-codex.sh`, so it links every skill in `plugins/`.
   3. Switch the marketplace in BOGA3's and Gnocchi's `.claude/settings.json`,
      and in BOGA3's docs, which point to the user-level skill.
   4. Re-run the Codex installer from engine, after uninstalling the old links.
   5. Archive `agent-skills`.

   Until step 3, `agent-skills` stays the live copy of `task-protocol`.
3. **Machine user.** Follow `docs/operator-setup.md`.

## Check

4. **Codex remote from the phone.** Codex 0.163 has
   `codex remote-control start | pair` (marked experimental). Check:
   - that a phone paired this way reaches a session started in tmux;
   - that it works on the Linux host too.
5. **Existing control planes.** Before building the SSH host layer, evaluate
   Paseo, Zeron and Pragma (`prior-art.md`).
6. **Model IDs.** `gpt-6.1-sol` is confirmed (it is this Mac's Codex
   default). Check `gpt-6-luna` with a one-line `codex exec`.
7. **Reviewer tier on Codex.** Claude Code can start a subagent on a
   different model. Check whether a Codex session can, or whether its
   reviewer inherits the session's model.

## Questions

The skill already follows the recommended answer to each of these. Say if one
should change.

8. **Planner draft in the issue from batch one** (`references/plan.md`,
   "Working with the operator"). *Recommended: yes.* You can read and answer
   from your phone.
9. **Labels stay the source of truth; issue types are set as well where the
   owner has them** (`references/plan.md`, "Feature issue"). *Recommended:
   yes.* Types are organisation-only, so a personal repo such as engine has
   none.
10. **The planner writes only to GitHub** (`references/plan.md`, "In-repo
    plans"). *Recommended: yes, for now.* `task-protocol` already writes
    in-repo plans. Revisit after the pilot.
11. **The in-repo mapping** (`references/plan-sources.md`). *Recommended: as
    written.* It matches how `task-protocol` runs BOGA3's `docs/plans`.
12. **In-repo bindings live in the target repo** (`references/plan-sources.md`,
    "Which mapping applies"). *Recommended: yes.* A repo's templates change
    with the repo, not with engine.
13. **`low` decisions get one collapsed line each in the PR body**
    (`references/rulings.md`). *Recommended: yes.* You never read them, but
    the evidence is there if a grade was wrong.
14. **A `high` ruling @mentions you and carries on, rather than stopping**
    (`references/rulings.md`). *Recommended: yes.* A class of `high` rulings
    that keeps being reversed becomes a hard stop.

Skill paths are relative to `plugins/engine/skills/engine/`.
