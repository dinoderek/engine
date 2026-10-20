---
name: executor
description: Execute one task card through to a merged PR, hands-off or gated. Use when told to execute a task (a tracker issue, owner/repo#<n>, or a repo card file) with the engine executor.
argument-hint: "<owner/repo#n | path/to/card.md> [hands-off | gated]"
---

# Engine executor

One task, one session, one worktree, one PR. This skill orders the work and
owns the executor's rules. The repo's `AGENTS.md` owns its commands: the
worktree command, the gate command, the PR template, the merge method and its
own extra hard-stop classes. Where it defines a step, it wins. Where it is
silent, use `git worktree add` from the default branch, the repo's standard
test command and a concise PR body with evidence, and say so in the session.

## Modes

The kickoff names the mode. If it names none, run **gated**.

| | Hands-off | Gated |
| --- | --- | --- |
| Open questions | Decide each, log a ruling | Present each with a recommendation, wait for the operator, log the answer as a ruling `Decided by: operator` |
| Hard stops | Stop and ask (below) | The same |
| Merge | On green, without asking | Say the PR is ready and wait for the operator's approval |
| After merge | Report and clean up | Also list the next ready tasks, each with a kickoff line |

Never enable auto-merge, and never merge with `--auto` or `--admin`.

## Card sources

The steps are the same for both sources; only the bookkeeping differs.

| | Issue card | Repo card file |
| --- | --- | --- |
| Card | The issue body: Outcome, Decided, Open, Tests, Docs to update, Requires, Harness / model | The file, in the repo's milestone and card format |
| Depends on | The issue's "blocked by" list (`gh api repos/<o>/<r>/issues/<n>/dependencies/blocked_by`) | The card's dependency line |
| Claim | `engine:running` plus the claim comment | None: the session and its branch are the claim |
| Rulings | One issue comment each, plus the PR body | The PR body only |
| Inbox | The issue's comments | The PR's comments, once it is open |
| Hard stop | `engine:needs-human` plus an @mention | Ask in the session |
| Close | `Closes #<task>` in the PR body | The PR deletes the card and marks it done in the milestone; the last card deletes the milestone |

**GitHub identity.** For an issue card, every `gh` call acts as engine's
machine user, so your @mentions notify the operator. If `GH_TOKEN` is unset,
prefix each call with `GH_TOKEN="$(cat ~/.config/engine/github-token)"`. No
token and no file: stop (environment). `git push` uses the checkout's own
credentials.

**The operator** is the feature issue's assignee, else its author.

## Steps

1. **Claim** (issue cards). If the task has `engine:running`,
   `engine:needs-human` or `engine:in-review`, stop and ask. If a blocker is
   still open, stop and ask. Add `engine:running`; a missing `engine:` label
   means the repo is not set up (environment). Post the claim comment. Then
   re-read the comments: if an earlier claim from another run exists, remove
   nothing, say so, and end the session.
2. **Open.** Unless you are already in the task's worktree, create one with
   the repo's command. The branch carries the session name
   (`<type>/<repo>-<task>-<slug>`, unless the repo says otherwise). Work only
   there.
3. **Load and check freshness.** Read the card, the feature issue and the
   comments on both (the inbox). Load what `AGENTS.md` routes to for the
   areas you touch. Check every assumption in the card against the default
   branch, merged sibling tasks included; a card the default branch has
   invalidated is a hard stop (drift). Settle the Open questions for your
   mode.
4. **Build** with tests, and update the owning docs.
5. **Gates.** Run the repo's gate command to green, including any lane that
   runs only locally.
6. **Review.** Spawn a fresh reviewer that did not write the diff, on the
   branch against the default branch, with `AGENTS.md` as its checklist. Its
   first line is `Verdict: APPROVED` or `Verdict: CHANGES_REQUESTED`. Fix
   every real finding and review again. Decline a finding only with a reason,
   which goes in the PR body. Two rounds that don't converge: hard stop
   (review).
7. **Gates again.**
8. **Checkpoint.** Read the inbox and act on anything new.
9. **PR.** Write the body per the repo's template: gate evidence, rulings,
   deviations, declined findings, and the close line. Push and open it. Swap
   `engine:running` for `engine:in-review`. If any ruling you made yourself is
   medium or high risk, add `engine:ruling-review`.
10. **Merge on green.** Watch the checks with `gh pr checks --watch`. On a
    failure, read the logs, fix, and return to step 5. If the default branch
    moved or conflicts, merge it in and return to step 5. When every check is
    green, read the inbox, then merge with the repo's merge method and
    `--match-head-commit <sha>`, where `<sha>` is the commit that was reviewed
    and gated. A repo with no CI merges on local gates alone. Gated: wait for
    the operator's approval before merging.
11. **Report** (issue cards). Post an outcome comment: what shipped, the
    rulings, the deviations. Update downstream tasks (below).
12. **Cleanup.** Run the repo's worktree-release command and say what it
    removed. A PR closed without merging: ask first. Then end the session.

Read the inbox at steps 3, 8 and 10, and first thing when resuming after a
stop.

## Claim comment

The first line is fixed so claims can be searched. Never invent a value: use
`none` when one is unknown. Values in the kickoff (`run=`, `tmux=`, `rc=`)
override these.

```text
Claim: <repo>-<task>
Run: <repo>-<task>-<UTC yyyymmddThhmmssZ>
Host: <logical host name>
Harness: <harness> <version> (<model>)
Mode: hands-off|gated
Started by: hand|engine start
Tmux: <session:window>|none
Remote Control: <name>|none
Branch: <branch>
```

- **Host** is the logical name in `~/.config/engine/host` (such as `mac` or
  `devbox`), never a hostname or an address. If the file is missing, stop
  (environment).
- **Tmux** comes from `tmux display-message -p '#S:#W'` when `$TMUX` is set.

## Rulings

Every ruling goes in the PR body's Rulings section and, for an issue card,
in one issue comment of its own. The first line is fixed so rulings can be
searched:

```text
Ruling (risk: low|med|high): <decision>
Why: <reason>
If wrong: <what it costs to undo, what it touches>
```

A ruling the operator made in gated mode ends with `Decided by: operator`.

You own these decisions, each logged as a ruling:

- internal design, naming and structure;
- which test layer a claim belongs in;
- choices the design left open;
- refactors inside the modules the task touches;
- splitting the task, or adding tasks inside the feature's scope;
- UI details within the agreed mocks;
- adding dev-only dependencies.

## Hard stops

Stop, and for an issue card swap `engine:running` for `engine:needs-human` and
comment with an @mention of the operator saying what you need. Then end your
turn and wait in the live session. On resuming, read the inbox, swap the label
back and continue.

1. **Product:** a product decision that neither the docs nor the requirements
   answer.
2. **Data:** a destructive or irreversible data change, such as a migration
   that drops or rewrites data, or a backfill.
3. **Security:** auth, permissions, row-level security, or secrets handling.
4. **Contract:** a change the design doesn't cover to a public API, schema,
   protocol or CLI surface that others use.
5. **Gates:** loosening a gate (a threshold, a suppression or a baseline
   entry).
6. **Outward or costly:** deploying, touching hosted environments, store
   releases, paid services, or sending messages.
7. **Drift:** the default branch has moved in a way that invalidates the card.
8. **Objectives:** the feature's requirements would have to change.
9. **Review:** two review rounds that don't converge.
10. **Environment:** a required gate, tool or setting this host lacks.

The repo's `AGENTS.md` adds its own classes.

## Changing the plan mid-flight

- Edit any task that has no state label (still to do).
- Never edit a task that is `engine:running` or `engine:needs-human`: comment
  on it instead.
- A new task, or one no longer needed, goes on tasks still to do, plus a
  `Deviation:` comment on the feature issue.

## Next ready tasks (gated)

- **Issue cards:** the feature's open sub-issues with no `engine:` state label
  and every blocker closed (`gh api repos/<o>/<r>/issues/<feature>/sub_issues`).
- **Repo card files:** fetch the default branch, read the milestone, and list
  the cards whose dependencies are done and that have no open PR.

For each, give a kickoff line, `Execute <task> with the engine executor,
<mode> mode`, and say which can run in parallel.
