# Execute one task

You run one task from its card to a merged PR. Wording here is in GitHub terms
("the task's issue", "comment"). With an in-repo plan, map each one through
`plan-sources.md`.

## Modes

The kickoff names the mode. If it names none, run **gated**.

| | Hands-off | Gated |
| --- | --- | --- |
| Open questions | Decide each, graded per `rulings.md` | Present each with a recommendation, wait for the operator, log the answer as a ruling ending `Decided by: operator` |
| Hard stops | Stop and ask (`rulings.md`) | The same |
| Merge | On green, without asking | Say the PR is ready and wait for the operator's approval |
| After merge | Report and clean up | Also list the next ready tasks, each with a kickoff line |

Never enable auto-merge or merge with `--admin`. In Claude Code the plugin's
merge guard blocks both, and any merge without `--match-head-commit`.

## GitHub identity

For a GitHub plan, every `gh` call acts as engine's machine user, so your
@mentions notify the operator. If `GH_TOKEN` is unset, run each one as
`engine gh <args…>`, which takes the token from the token file without it
appearing on the command line. With no token and no file,
stop (**Environment**). `git push` uses the checkout's own credentials.

**The operator** is the feature issue's assignee, else its author.

## Steps

1. **Claim** (GitHub plans). First check, without changing anything:
   - the task has no `engine:running`, `engine:needs-human`,
     `engine:in-review` or `engine:failed`, and a standalone task is
     `engine:approved`, not `engine:prepare`;
   - every blocker is closed
     (`gh api repos/<o>/<r>/issues/<n>/dependencies/blocked_by`);
   - the token exists, and the host file unless the kickoff names `host=`;
   - the repo has the `engine:` labels (`engine labels sync`).

   If a check fails, say so in the session only. Otherwise add
   `engine:running` and post the claim comment (below). Then re-read the
   comments. If an earlier claim from another run exists, delete your claim
   comment, say so, and end the session (step 12's last action).
2. **Open.**
   - `engine version` must run, because the merge guard calls `engine`
     (**Environment**).
   - Read the card's `## Overrides` and the feature's. They are the only repo
     rules you may set aside. Any other repo rule that tells you to involve
     the operator is a **Repo rule** hard stop.
   - Unless you are already in the task's worktree, create one with the
     repo's worktree command, and work only there. The branch carries the
     session name (`<type>/<repo>-<task>-<slug>`, unless the repo says
     otherwise).
3. **Load and check freshness.**
   - Read the card, the feature issue, and the comments on both.
   - Load what `AGENTS.md` routes to for the areas you touch.
   - Check every assumption the card makes against the current default
     branch: code, docs, and sibling tasks that have merged. If the card no
     longer holds, that is a **Drift** hard stop.
   - In-repo plan: if the card's dependencies are not done, stop and ask.
   - Settle the Open questions for your mode.
4. **Build.** Implement the outcome with the tests the card lists, in the
   layers it names. Update the docs the card lists, plus any other owning doc
   the change touches.
5. **Gates.** Run the repo's gate command for this diff, plus the card's
   **Gates**, to green. That includes lanes that only run locally.
6. **Review.** Spawn a fresh reviewer that has not seen your work, in this
   skill's `review` mode (`review.md`). Give it the card and the branch diff
   against the default branch.
   - Fix every real finding, re-run the gates the fixes touch, and review
     again with a new reviewer.
   - Decline a finding only with a reason, which goes in the PR body.
   - Two rounds that don't converge are a **Review** hard stop.
7. **Gates again,** if anything changed since step 5.
8. **Checkpoint.** Read new comments on the task's issue, and act on them.
9. **PR.** Write the body with the repo's template, and add:
   - the gate evidence (commands run, results);
   - the `med` and `high` rulings, then the collapsed `low` ones;
   - deviations from the card, and declined review findings;
   - `Closes #<task>`, or, with an in-repo plan, delete the card and mark it
     completed in the milestone in this PR.

   Push and open the PR. Swap `engine:running` for `engine:in-review`.
   Release any local resources the repo says to stop when a PR opens.
10. **Merge on green.** Watch the PR's checks with `gh pr checks --watch`.
    - Take the merge lock (below) first. A standalone task needs none.
    - On a failure, read the logs, fix, and go back to step 5.
    - If the default branch moved, merge it in and go back to step 5. Keep
      the lock if it merged cleanly; a conflict you resolved is a fix.
    - Before merging, read the task's comments once more.
    - When every check is green, merge with the repo's merge method and
      `--match-head-commit <sha>`, where `<sha>` is the commit you reviewed
      and gated.
    - A repo with no CI merges on the local gates alone.
    - In gated mode, wait for the operator's approval before merging.
11. **Report.** Remove `engine:in-review`: the merge closed the task. Comment
    on the task's issue: what shipped, the `med` and `high` rulings, and the
    deviations. Update downstream tasks that are still to do if your work
    changed what they assume (below).
12. **Clean up.** Run the repo's worktree-release command from outside the
    worktree, and say what it removed. If the PR closed without merging, ask
    first. Then end the session. In hands-off mode, when the kickoff names
    `tmux=`, your last action is `cd "$HOME" && tmux kill-window -t
    "$TMUX_PANE"`: it closes your window and frees its slot. If the shell
    says its working directory was deleted, run it again. In gated mode the
    operator is reading, so leave it open.

Read the inbox (the task's comments) at steps 3, 8 and 10, and first thing
when you resume after a stop.

**Unattended sessions.** When `ENGINE_UNATTENDED=1` is in your environment,
nobody watches your screen. Treat any denied permission request as the
**Permission** hard stop (`rulings.md`).

## Merge lock

One task per feature merges at a time, so no sibling moves the default
branch under your last gate run. To take the lock, add `engine:merging` to
your task if no sibling carries it. The holder is the carrier whose latest
`engine:merging` `labeled` event (`gh api repos/<o>/<r>/issues/<n>/events`)
is oldest; check after adding it and just before merging, and if it is not
you, remove yours. Without the lock, check every 5 minutes.

Remove it after the merge, at a hard stop, while a gated run waits for
approval, while you make a fix, and when the PR closes unmerged. Take it
again to resume. A gated run whose head changed meanwhile asks for
approval again.

## Claim comment

The first line is fixed, so claims can be searched. Never invent a value: use
`none` when one is unknown. Values in the kickoff (`run=`, `tmux=`, `rc=`,
`host=`, `started=`) override these. Branch is the one step 2 opens, or the one you are on.

```text
Claim: <repo>-<task>
Run: <repo>-<task>-<UTC yyyymmddThhmmssZ>
Host: <logical host name>
Harness: <harness> <version> (<model>)
Mode: hands-off|gated
Started by: hand|engine start|coordinator
Tmux: <session:window>|none
Remote Control: <name>|none
Branch: <branch>
```

- **Host** is the logical name in `~/.config/engine/host` (such as `mac` or
  `devbox`), never a hostname or an address. If the file is missing and the
  kickoff names no `host=`, stop (**Environment**).
- **Tmux** comes from `tmux display-message -p '#S:#W'` when `$TMUX` is set.

## Changing the plan mid-flight

- You may edit any task that has no state label (still to do).
- Never edit a task that is `engine:running` or `engine:needs-human`. Comment
  on it instead: its comment thread is its inbox.
- A plan-level change, such as a new task or one that is no longer needed, is
  made on tasks still to do, plus a `Deviation: <what and why>` comment on the
  feature issue.

## Labels you set

- `engine:running` when you claim the task. On a standalone task it replaces
  `engine:approved`.
- `engine:needs-human` during a hard stop. It replaces `engine:running` or
  `engine:in-review`, and you restore that label when the stop is answered.
- `engine:in-review` when the PR opens, removed once the merge closes the
  task, so a closed task carries no state label.
- `engine:merging` while you hold the merge lock (step 10).
- `engine:ruling-review` with a `high` ruling. The operator clears it.

Leave every other label to the coordinator.

## Next ready tasks (gated)

- **GitHub plans:** the feature's open sub-issues that have no `engine:`
  state label and whose blockers are all closed
  (`gh api repos/<o>/<r>/issues/<feature>/sub_issues`).
- **In-repo plans:** fetch the default branch, read the milestone, and list
  the cards whose dependencies are done and that have no open PR.

For each one, give a kickoff line, `Execute <task> with the engine executor,
<mode> mode`, and say which ones can run in parallel.
