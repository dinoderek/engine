# Project context guideline

> **Owns:** what a repo must provide so executors can run hands-off, and the rules the seed prompt applies. **Not here:** what the executor does (the `build` skill, `plugins/engine/skills/build/`), the protocol's phases and states (`execution-protocol.md`).

The guideline is language-neutral. It names outcomes and registries, and the
seed prompt picks the tools for each stack.

## Core (every repo)

### 1. Entrypoint

- `AGENTS.md` is the single entrypoint; `CLAUDE.md` symlinks to it. **Rules
  live in the file; links are for depth, never for rules.**
- At most 1000 words: Non-negotiables (with the repo's extra hard-stop
  classes), Always load, Load on demand, Docs, Workflow.

### 2. Loading

- **Always load:** brief (what, state, scope, stack), architecture, project
  structure, testing strategy, quality gates. With `AGENTS.md` the set is
  capped at about 4000 words by the check.
- **Load on demand:** a trigger table in `AGENTS.md` ("if your task… load…")
  is the **only** routing: the README points to `AGENTS.md` and keeps no doc
  table. Every persistent doc appears in it or in always-load, and the check
  enforces that.
- **Persistent docs** are every tracked Markdown file outside the ephemeral
  (`design/`) and exempt directories (such as `.github/`) the docs registry
  names. The entrypoints need no index row. Tracker references are banned outside
  the ephemeral directories.
- **Doc header:** one line, `> **Owns:** … **Not here:** …`, under the title
  of every persistent doc. It stops duplication and carries no "load when",
  which would restate the index.
- **Links inside a doc** say when to follow them. No unconditional "see also".

### 3. One command per job

A single script (e.g. `./dev`) has these verbs:

- `doctor`: sets up the machine, turning "can't run" into a fix;
- `run`;
- `test [lane | for <paths>]`: `for` runs the required lanes whose trigger
  patterns in the lane registry match the paths;
- `worktree create | release`;
- `deploy`;
- `deps`.

A verb the repo has no use for, such as `deploy` with nothing to deploy, is
left out, and the usage says so. Run with no verb, the script prints that
usage and does nothing else: engine reads it. Docs cite verbs, not commands.

### 4. Worktrees

- Always work in a worktree, never in the main checkout. One task, one
  worktree, one branch, one PR.
- `worktree create` branches from the remote's current default branch and
  provisions anything the task needs. Given a branch that already exists
  locally, it checks that branch out as it is: engine sets it to a resumed
  task's work first. It puts the worktree outside the checkout, so the
  checkout's tools and `git status` never walk it. `worktree release` tears it
  all down. Engine runs `dev worktree create <branch>` when the repo has an
  executable `dev` at its root whose usage, printed with no verb, lists
  `worktree create`; otherwise, including when `dev` prints no usage within
  three minutes, it uses a plain `git worktree add`, which provisions nothing.
- Commit work in progress rather than stashing: the stash is shared across
  worktrees.

### 5. Quality gates

- The lanes are listed in a registry (`lanes.tsv`). `./dev test` runs them in
  parallel with no fail-fast and writes `.test-ai-results/summary.md`.
- **CI runs the same command.** A lane CI cannot run, such as one that needs a
  simulator, runs locally with its evidence in the PR. Merge on green means
  local gates plus every CI check.
- **Few operator checkpoints.** A rule that makes an agent involve the
  operator mid-task (agreeing lanes, approving a merge) blocks a hands-off
  run; Prepare finds it and the operator changes it or overrides it.
- Default targets:

| Target | Default |
| --- | --- |
| Coverage | 80% lines, 80% branches; a stack with no branch coverage (Go) gates statements. |
| Per function | cognitive complexity ≤ 25, ≤ 200 lines, nesting ≤ 4, ≤ 5 parameters |
| Per file | ≤ 500 lines of code or config; docs have word budgets instead |
| Import direction | No cycles, plus layer rules (`import-rules.tsv`); where the compiler rejects cycles (Go), the lane checks layers only. |
| Always | Lint, format, types, docs check |

- Existing violations go in baselines that only shrink: a row the code no
  longer needs fails its lane until lowered or deleted. An inline suppression
  needs a row. Loosening a target is a hard stop.

### 6. Run, test, deploy

- `./dev` verbs for agents; one runbook for humans, covering hosted
  operations and release.
- A testing-strategy doc that says which layer a claim belongs in.

### 7. Structure and modularisation

- The project-structure doc says where things live and where new code goes.
- The import-rules lane enforces module boundaries; prose does not.

### 8. Tech stack

- Summarised in the brief. Every version has one source file (a lockfile,
  say); docs cite it, never restate it.
- The gates run on exactly that version, locally and in CI: the gate command
  takes it from the source file, and the versions lane fails when anything
  runs another. Dev tools are pinned in the stack's own lockfile where it
  allows, so the dependency bot bumps them too.

### 9. Docs discipline

- **Budgets:** every persistent doc has a word budget in `doc-budgets.tsv`
  (its own or the default). A doc over budget is split or trimmed, never given
  a bigger budget without the operator; adding or widening an exempt,
  ephemeral or set row loosens the check the same way.
- **One place per fact:** docs cite the registry or code that is the source.
- **The docs check** verifies budgets (per doc, and for the always-load set,
  which must match `AGENTS.md`'s list), that links resolve and cited paths
  exist (files, never lines), the headers, that the index is complete and
  `CLAUDE.md` is a symlink, that every cited gate lane exists, and that no
  file holds a tracker reference (§10) or cites a file in an ephemeral
  directory. It is written in the repo's own stack.

### 10. Current state only, no chronicle

- Docs describe how things are: no "previously", no dated entries, no
  changelog. History is in git and the PRs.
- Plans and task cards live in the engine server, never in the repo. Docs and
  code never cite an issue, a PR or a task. The docs check greps for tracker
  references: issue and PR numbers (alone, after `owner/repo`, or after a word
  like "issue" or "PR"), issue and PR URLs, and plan file paths. Commit
  messages and PR bodies may cite them.
- Drafts that must sit in the repo live in one ephemeral directory, exempt
  from every doc rule and never cited.

### 11. Dependency freshness

- **Point in time:** `./dev deps` reports outdated packages and audit
  findings. A `versions.md` doc says where each version lives and states the
  upgrade policy, e.g. never more than one minor version behind.
- **Continuous:** Dependabot or Renovate, weekly, grouped, with a cooldown. A
  versions lane fails on drift between pins. Patch and minor updates merge on
  green, as executor PRs do.

## Optional modules

| Module | Adopt when | Mechanism |
| --- | --- | --- |
| Product facts | Agents keep re-deciding product behaviour | One stable ID per decision (`<subject>.<slug>`), with kind, status, why, code. Fact tables run as tests. An `open` fact is a hard stop. |
| Design system | The repo has a UI | A catalog of **which component to reach for**, never its props: the component file and its tests are the spec. The check fails when a component folder is missing from the catalog. Tokens are the only source of colour, spacing and type, and a lint ratchet bans literals. |
| Isolated environments | The repo has stateful infrastructure (DBs, ports, simulators) | A slot lease per worktree, with derived ports. Commands that need the lease refuse to run without it. |
| Test-directory READMEs | Test layers have local conventions | Read a directory's README before editing its tests. |

## Seed prompt

- **New repo:** interview the operator (stack, deploy target, stateful
  infrastructure, UI, product decisions); generate the core plus the modules
  that apply, with the docs check, lanes, CI and dependency bots; open one PR.
- **Retrofit:** audit an existing repo against this guideline and report the
  gaps as a plan with tasks, ready for the protocol.
