# Execute one task

You run one task from its card to a merged PR, without the operator unless a
hard stop (`rulings.md`) needs them. Wording here is in GitHub terms ("the
task's issue", "comment"). With an in-repo plan, map each one through
`plan-sources.md`.

## Start

The coordinator starts you in the task's worktree root. If you were started
anywhere else (by hand, for a pilot), first create a worktree with the repo's
worktree command and work only there.

## Steps

1. **Load and check freshness.** Read the card, the feature issue, and the
   comments on both. Check every assumption the card makes against the current
   default branch: code, docs, and sibling tasks that have merged. If the card
   no longer holds, that is a **Drift** hard stop.
2. **Build.** Implement the outcome with the tests the card lists, in the
   layers it names. Update the docs the card lists, plus any other owning doc
   the change touches.
3. **Gates.** Run the repo's gate command for this diff, plus the card's
   **Gates**, to green. That includes lanes that only run locally. Don't ask
   the operator to agree lanes: the card is the agreement.
4. **Review.** Spawn a fresh reviewer that has not seen your work, with the
   `review` mode of this skill (`review.md`). Give it the card and the branch
   diff against the default branch. Fix every real finding, re-run the gates
   the fixes touch, and review again with a new reviewer. Two rounds that
   don't converge are a **Review** hard stop. Note any finding you decline,
   and why.
5. **Gates again,** if anything changed since step 3.
6. **Checkpoint.** Read new comments on the task's issue, and act on them.
7. **PR.** Write the body with the repo's template and add:
   - the gate evidence (commands run, results);
   - the `med` and `high` rulings, then the collapsed `low` ones;
   - deviations from the card, and declined review findings;
   - `Closes #<task>`, or, with an in-repo plan, delete the card and mark it
     completed in the milestone in this PR.

   Set `engine:in-review` on the task. Release any local resources the repo
   says to stop when a PR opens.
8. **Merge on green.** Watch the PR's checks with `gh pr checks --watch`.
   - On a failure, read the logs, fix, and go back to step 3.
   - If the default branch moved or conflicts, merge it in and go back to
     step 3.
   - Before merging, read the task's comments once more.
   - When every check is green, merge with
     `gh pr merge --match-head-commit <sha>`, where `<sha>` is the commit you
     reviewed and gated. Use the merge method the repo asks for.
   - A repo with no CI merges on the local gates alone.
9. **Report.** Comment on the task's issue: what shipped, the `med` and
   `high` rulings, and the deviations. Update downstream `todo` tasks if your
   work changed what they assume (below).
10. **Clean up.** Run the repo's worktree-release command, then end the
    session.

## Changing the plan mid-flight

- You may edit any task that is still `todo`.
- Never edit a task that is `running` or `needs-human`. Comment on it
  instead: its comment thread is its inbox.
- A plan-level change, such as a new task or one that is no longer needed, is
  made on `todo` tasks, plus a `Deviation: <what and why>` comment on the
  feature issue.

## Labels you set

`engine:needs-human` (a hard stop) and back to `engine:running` once it is
answered, `engine:in-review` when the PR opens, and `engine:ruling-review`
with a `med` or `high` ruling. The merge closes the task. Leave every other
label to the coordinator.
