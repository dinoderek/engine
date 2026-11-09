---
name: build
description: Build one task to a merged PR in any repo, outside the engine process. Use when the operator asks you to build, implement or ship one task, issue or change in this repo through review to a merged PR (gated by default, hands-off on request), or when you are spawned as a reviewer of another session's diff (`review`).
argument-hint: "[<task>] [gated | hands-off] | review"
---

# Build

You take one task from its card, issue or request to a PR merged on green.
One task = one session = one worktree = one branch = one PR.

## Modes

The kickoff names the mode. If it names none, run **gated**.

| | Gated (default) | Hands-off |
| --- | --- | --- |
| Open questions | Present each with your recommendation; wait for the answer | Decide each yourself |
| Hard stops | Ask in the session and wait (`references/hard-stops.md`) | The same |
| Merge | Say the PR is ready, with its head SHA, and wait for approval | Merge on green, without asking |

`review`: you are a reviewer, not a builder. Load `references/review.md` and
follow only it.

## Rules

1. **Work from the target repo.** Read its `AGENTS.md` (or `CLAUDE.md`) and
   everything it says to always load, before anything else.
2. **The repo's rules win.** Take commands, paths, gates, worktree verbs and
   the PR template from the repo. Only an override the operator agreed
   (`## Overrides` on the card) sets a repo rule aside. A repo rule that tells
   you to involve the operator is a hard stop unless an override covers it.
   Where the repo is silent, use `git worktree add` from the default branch,
   its standard test command and a concise PR body, and say so in the PR.
3. **Plans are never cited.** Code, tests and docs never mention an issue
   number, plan path or task ID. Commit messages and PR bodies may.
4. **Durable decisions graduate** into the repo's owning doc, in the PR that
   ships them.
5. **GitHub is the host's `gh`,** with its configured sign-in and the host's
   normal Git configuration.
6. **Never fake evidence.** A lane you did not run is not passed.

## Steps

1. **Open.** `engine version` must run: the plugin's merge guard calls it
   (**Environment** hard stop). Create a worktree from the remote's default
   branch with the repo's command and work only there. If your harness opened
   one, merge the default branch into it first. Commit work in progress;
   never use a bare `git stash`, which other sessions share.
2. **Load and check.** Read the card and any thread on it. Load what
   `AGENTS.md` routes to for the areas you touch. Check every assumption the
   card makes against the current default branch: code, docs, merged sibling
   work. A card that no longer holds is a **Drift** hard stop.
3. **Decide.** Settle the open questions for your mode (Decisions, below).
4. **Build.** Implement the outcome with the tests the card lists, in the
   layers the repo's testing strategy assigns. Update the docs the card
   lists, and every other owning doc the change touches.
5. **Gates.** Run the repo's full gate command, plus the card's **Gates**, to
   green, including lanes that run only locally. A lane that fails on the
   default branch itself, regardless of your diff, is a hard stop asked
   about the default branch (`references/hard-stops.md`), not something to
   work around.
6. **Review.** Spawn a fresh reviewer that has not seen your work, in `review`
   mode, on the model the kickoff names (`Reviewer model: <id>`) if the
   harness lets you choose; otherwise it inherits yours. Give it the card and
   the branch diff against the remote default branch, with the repo's
   entrypoint as its checklist.
   - Fix every real finding, rerun the gates the fixes touch, and review again
     with a new reviewer.
   - Decline a finding only with a reason; it goes in the PR body.
   - Repeat until `Verdict: APPROVED`. Stop only for a **Review** hard stop.
7. **Refresh.** Fetch. If the default branch moved, merge it in and go back
   to step 5. Rerun the gates if anything changed since they last passed.
8. **PR.** Push and open the PR with the repo's template. Add the gate
   evidence (commands and results), any `low` notes, deviations from the
   card, declined findings, and truthful harness and model attribution.
9. **CI.** Watch with `gh pr checks --watch`. On a failure, read the logs and
   the repo's result artifacts, fix, and go back to step 5. A repo with no
   CI merges on the local gates alone.
10. **Merge.** The head must be the commit that was reviewed and gated.
    - Gated: say the PR is ready, with its URL and head SHA, and wait for the
      operator's explicit approval. If the head changes after that, ask again.
    - Hands-off: merge on green.
    - Merge with a command of its own, `gh pr merge <n> -R <owner/repo>
      --<method> --match-head-commit <sha>`, using the repo's merge method.
      Run any base or head check as a separate command first: an allow rule
      matches only a command that starts with its prefix.
    - Never enable auto-merge or merge with `--admin`. The merge guard blocks
      both, any merge without `--match-head-commit`, and merges through the
      API.
11. **Clean up.** Report what shipped and every deviation. Run the repo's
    worktree-release command from outside the worktree and say what it
    removed. If the PR closed unmerged, a gated run asks before releasing.

When the operator writes to you mid-task, read it before your next step and
act on it.

**Unattended sessions.** With `ENGINE_UNATTENDED=1` in your environment,
nobody watches your screen. A denied permission request with no route that
needs no new permission is the **Permission** hard stop: raise it with the
exact tool, input and rule, a denied merge too.

## Decisions

You decide, without asking:

- internal design, naming and structure;
- which test layer a claim belongs in;
- the card's **Open** questions, and choices its design left open;
- refactors inside the modules the task touches;
- UI details within the agreed mocks;
- adding dev-only dependencies.

None of these is a ruling, and none needs a note. For example, renaming a
helper, testing a parser in a unit test rather than end to end, splitting a
function to meet a lint target, or picking one of the options the card's
Open list offers: the diff shows each, and a reviewer judges it there.

**`low` notes are optional.** At most five one-line notes in the PR body,
under a collapsed `<details><summary>Low-risk decisions</summary>`, and only
for something a reviewer would otherwise ask about. No notes is the normal
case. Anything bigger than a note is either a hard stop or, in an engine
server task, a `review` ruling (the `engine` skill).
