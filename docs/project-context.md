# Project context guideline

> **Owns:** what a repo must provide so executors can run hands-off, and the rules the seed prompt applies. **Not here:** the protocol the executor follows (`execution-protocol.md`).

The guideline is language-neutral. It names outcomes and registries, and the
seed prompt picks the tools for each stack. Lineage: Gnocchi is the core
template, and BOGA3 supplies the optional modules.

## Core (every repo)

### 1. Entrypoint

- `AGENTS.md` is the single entrypoint, and `CLAUDE.md` is a symlink to it.
- **Rules live in the file; links are for depth, never for rules.**
- At most 1000 words. Sections: Non-negotiables, Always load, Load on demand,
  Docs, Workflow, extra hard stops.

### 2. Loading

- **Always load:** brief (what, state, scope, stack), architecture, project
  structure, testing strategy, quality gates. The set, with `AGENTS.md`, is
  capped at about 4000 words, and the check enforces the cap.
- **Load on demand:** a trigger table in `AGENTS.md` ("if your task… load…").
  It is the **only** routing. Every persistent doc appears in it or in
  always-load, so there are no orphans. The check enforces that.
- **Doc header:** one line, `Owns: … Not here: …`. It is for the agent that
  has already arrived, and its job is to stop duplication. It carries no
  "load when", because that would restate the index.
- **Links inside a doc** say when to follow them. No unconditional "see also".

### 3. One command per job

A single script (e.g. `./dev`) has these verbs:

- `doctor`: sets up the machine, and turns "can't run" into a gap to fix;
- `run`;
- `test [lane | for <paths>]`: `for` runs the required lanes whose trigger
  patterns in the lane registry match the paths;
- `worktree create | release`;
- `deploy`;
- `deps`.

A verb the repo has no use for yet, such as `deploy` with nothing to deploy,
is left out, and the script's usage and the docs say so. Docs cite the verbs
and never restate the commands behind them.

### 4. Worktrees

- Always work in a worktree, never in the main checkout. One task, one
  worktree, one branch, one PR.
- `worktree create` branches from the remote's current default branch and
  provisions anything the task needs. It puts the worktree outside the
  checkout, so the checkout's tools and `git status` never walk it.
  `worktree release` tears it all down.
- Commit work in progress rather than stashing it, because the stash is shared
  across worktrees.

### 5. Quality gates

- The lanes are listed in a registry (`lanes.tsv`). `./dev test` runs them in
  parallel with no fail-fast and writes `.test-ai-results/summary.md`.
- **Where there is CI, it runs the same command.** A lane CI cannot run, such
  as one that needs a simulator, runs locally, and its evidence goes in the PR.
  Merge on green means local gates plus every CI check.
- Default targets:

| Target | Default |
| --- | --- |
| Coverage | 80% lines, 80% branches. A stack with no branch coverage (Go) gates statements, and its quality-gates doc says so. |
| Per function | cognitive complexity ≤ 25, ≤ 200 lines, nesting ≤ 4, ≤ 5 parameters |
| Per file | ≤ 500 lines of code or config; docs have word budgets instead |
| Import direction | No cycles, plus layer rules (`import-rules.tsv`). Where the compiler already rejects cycles (Go), the lane checks the layers only. |
| Always | Lint, format, types, docs check |

- Existing violations go in baselines that only shrink: a row the code no
  longer needs fails its lane until it is lowered or deleted. An inline
  suppression (a `nolint`-style comment) counts as a violation and needs a
  row. Loosening a target is a hard stop.

### 6. Run, test, deploy

- `./dev` verbs for agents.
- One runbook for humans, covering hosted operations and release.
- A testing-strategy doc that says which layer a claim belongs in.

### 7. Structure and modularisation

- The project-structure doc says where things live and where new code goes.
- Module boundaries are enforced by the import-rules lane, not described in
  prose.

### 8. Tech stack

- Summarised in the brief.
- Every version has exactly one source file (`.python-version`, a lockfile,
  and so on). Docs cite the file and never restate the version.
- The gates run on exactly that version, locally and in CI: the gate command
  takes it from the source file, and the versions lane fails when anything
  runs another. Dev tools are pinned in the stack's own lockfile where it
  allows that, so the dependency bot bumps them too.

### 9. Docs discipline

- **Budgets:** every indexed doc has a word budget in `doc-budgets.tsv`. A doc
  over budget is split or trimmed, never given a bigger budget without the
  operator.
- **One place per fact.** Where a registry or the code is the source, docs cite
  it.
- **The docs check** verifies:
  - budgets, per doc and for the always-load set;
  - that links resolve and cited paths exist;
  - the doc headers;
  - that the index is complete;
  - that there are no ephemeral references.

### 10. Current state only, no chronicle

- Docs describe how things are. No "previously", no "we changed X to Y", no
  dated entries, no changelog sections. History is in git and the PRs.
- Plans, designs and task cards live in the tracker. Docs and code never cite
  an issue, a PR or a task. The docs check greps for tracker references.

### 11. Dependency freshness

- **Point in time:** `./dev deps` reports outdated packages and audit findings.
  A `versions.md` doc says where each version lives and states the upgrade
  policy, e.g. never more than one minor version behind on runtime and
  toolchain.
- **Continuous:** Dependabot or Renovate, weekly, with updates grouped and a
  cooldown. A versions lane fails on drift between pins.
- Patch and minor updates merge on green, the same way executor PRs do.

## Optional modules

| Module | Adopt when | Mechanism |
| --- | --- | --- |
| Product facts | Agents keep re-deciding product behaviour | One stable ID per decision (`<subject>.<slug>`), with kind, status, why, code. Fact tables run as tests. An `open` fact is a hard stop. |
| Design system | The repo has a UI | A catalog of **which component to reach for**, never its props: the component file and its tests are the spec. The check fails when a primitive or component folder is missing from the catalog. Tokens are the only source of colour, spacing and type, and a lint ratchet bans literals. |
| Isolated environments | The repo has stateful infrastructure (DBs, ports, simulators) | A slot lease per worktree, with derived ports. Commands that need the lease refuse to run without it. Only the owner tears down. |
| Test-directory READMEs | Test layers have local conventions | Editing tests in a directory means reading its README first. |

## Seed prompt

The seed prompt has two modes:

- **New repo:**
  1. Interview the operator: stack, deploy target, stateful infrastructure,
     UI, product decisions.
  2. Generate the core, plus whichever modules apply.
  3. Implement the docs check, the lanes, CI and the dependency bots.
  4. Open the result as one PR.
- **Retrofit:** audit an existing repo against this guideline, and report the
  gaps as a feature with tasks, ready for the protocol.
