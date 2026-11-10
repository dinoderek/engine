# Project context guideline

> **Owns:** what a repo must provide so executors can run hands-off. **Not here:** what the executor does (the `build` skill, `plugins/engine/skills/build/`), the protocol's phases and states (`execution-protocol.md`).

The guideline is language-neutral. It names outcomes and registries; each
repo picks the tools for its stack.

## Core (every repo)

### 1. Entrypoint and loading

- `AGENTS.md` is the single entrypoint; `CLAUDE.md` symlinks to it. **Rules
  live in the file; links are for depth, never for rules.** At most 1000
  words: Non-negotiables (with the repo's extra hard-stop classes), Always
  load, Load on demand, Docs, Workflow.
- **Always load:** brief, architecture, project structure, testing strategy,
  quality gates: about 4000 words with `AGENTS.md`, at most.
- **Load on demand:** a trigger table in `AGENTS.md` ("if your task… load…")
  is the **only** routing; the README keeps no doc table. Every persistent
  doc appears in it or in always-load.
- **Persistent docs** are every tracked Markdown file outside the ephemeral
  (`design/`) and exempt (`.github/`) directories. Each opens with one
  `> **Owns:** … **Not here:** …` line; its links say when to follow them.

### 2. One command per job

A single script (e.g. `./dev`) has the verbs `doctor` (turns "can't run"
into a fix), `run`, `test [lane | for <paths>]`, `worktree create | release`,
`deploy` and `deps`. A verb with no use is left out, and the usage says so.
Run with no verb, the script prints that usage and does nothing else: engine
reads it.

### 3. Worktrees

- Work in a worktree; commit work in progress rather than stashing, because
  the stash is shared.
- `worktree create` branches from the remote's current default branch,
  provisions what the task needs, and checks out a branch that already exists
  locally as it is. `worktree release` tears it down and is idempotent.
- Engine runs `dev worktree create <branch>` when the repo's executable `dev`
  at its root lists `worktree create` in the usage it prints with no verb
  (within three minutes); otherwise a plain `git worktree add`. When a task
  is done, it runs `dev worktree release` if the usage lists it, then
  `git worktree remove` on any worktree left, which keeps the branch.

### 4. Quality gates

- The lanes are listed in a registry (`lanes.tsv`). `./dev test` runs them in
  parallel, with no fail-fast, and writes `.test-ai-results/summary.md`. **CI
  runs the same command.** A lane CI cannot run runs locally, with its
  evidence in the PR. Merge on green means local gates plus every CI check.
- A rule that makes an agent involve the operator mid-task (agreeing lanes,
  approving a merge) blocks a hands-off run; Prepare finds it, and the
  operator changes or overrides it.
- Default targets: coverage 80% of lines and of branches (statements where
  the stack has no branch coverage, as in Go); per function, cognitive
  complexity ≤ 25, ≤ 200 lines, nesting ≤ 4, ≤ 5 parameters; per file, ≤ 500
  lines; no import cycles, plus layer rules (where the compiler rejects
  cycles, as in Go, the lane checks layers only); lint, format, types and the
  docs check.
- Existing violations go in baselines that only shrink; an inline suppression
  needs a row; loosening a target is a hard stop.
- A testing-strategy doc says which layer a claim belongs in; a
  project-structure doc, where new code goes; one runbook for humans covers
  hosted operations and release.

### 5. Versions and freshness

Every version has one source file (a lockfile, say) that docs cite, never
restate. The gates run on exactly that version, locally and in CI, and a
versions lane fails when anything runs another or the pins drift apart. Dev
tools are pinned in the stack's own lockfile where it allows, so the
dependency bot bumps them. `./dev deps` reports outdated packages and audit
findings; a `versions.md` doc says where each version lives and the upgrade
policy. Dependabot or Renovate runs weekly, grouped, with a cooldown; patch
and minor updates merge on green.

### 6. Docs discipline

- **Budgets:** every persistent doc has a word budget in `doc-budgets.tsv`.
  A doc over budget is split or trimmed, never given a bigger budget without
  the operator; adding or widening an exempt, ephemeral or set row loosens the
  check the same way.
- **One place per fact:** docs cite the registry or code that is the source.
- **A docs check**, written in the repo's own stack, verifies budgets, the
  always-load set, links and cited paths, headers, the index, cited gate
  lanes, and tracker and ephemeral references.
- **Current state only:** no "previously" or changelog; history is in git and
  the PRs. Plans and task cards live in the engine server, never in the repo;
  docs and code never cite an issue, a PR or a task (commit messages and PR
  bodies may). Drafts live in one ephemeral directory, exempt from every doc
  rule and never cited.

## Optional modules

- **Product facts**, when agents keep re-deciding product behaviour: one
  stable ID per decision (`<subject>.<slug>`) with kind, status, why and code,
  run as tests; an `open` fact is a hard stop.
- **Design system**, when the repo has a UI: a catalog of **which component to
  reach for**, never its props (the component and its tests are the spec),
  checked for completeness. Tokens are the only source of colour, spacing and
  type, and a lint ratchet bans literals.
- **Isolated environments**, for stateful infrastructure: a slot lease per
  worktree, with derived ports; commands that need it refuse to run without
  it.
- **Test-directory READMEs**, for local test conventions: read one before
  editing that directory's tests.
