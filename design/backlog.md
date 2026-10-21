# Backlog (ephemeral)

Work to do before or alongside the build. Design questions are marked **Q**
inline in the doc that owns them. Delete each item here when it is done.

## Set up

1. **Pilot task.** One task in BOGA3, chosen by the operator. BOGA3 needs no
   changes: the planner overrides its blocking rules in the card.
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

10. **Where you review a planner draft.** You need to annotate the text with
    comments, and a GitHub issue body cannot take inline comments. The plan
    still ends up in GitHub issues; only the draft surface is open.
    Candidates:
    - **Claude Docs:** comments anchored to the text, on the web and the
      phone, with replies in the thread. It works only with Claude, and the
      approved text is copied into the issue.
    - **A draft PR holding the plan as a markdown file** on a branch that is
      never merged: line comments, GitHub-native, any harness. Comments land
      on source lines, not on the rendered text.
    - **A local file in your editor** with inline notes: works, but not from
      the phone.

    Until this is decided, `references/plan.md` drafts in the issue body.
