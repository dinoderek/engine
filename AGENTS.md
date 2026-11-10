# AGENTS.md

> **Owns:** the rules every session in this repo follows, and the index of every doc. **Not here:** the protocol a task follows (`docs/execution-protocol.md`), how each gate works (`docs/quality-gates.md`).

`CLAUDE.md` links here. **Links are for depth, never rules.**

## Non-negotiables

1. **Every gate is mandatory.** Get `./dev test` green before every PR; CI
   runs the same command. A lane that cannot run here is a setup gap: fix it
   with `./dev doctor`, never skip it. Never report an unrun lane as
   passed, or fake evidence.
2. **Tests always.** Every code change adds or updates tests for the
   behaviour it changes, in the layer `docs/testing-strategy.md` assigns. A
   gate check gets a test proving it fails on a violation.
3. **Quality targets are gates, not advice.** `./dev test` enforces:
   - coverage **≥ 80% of statements**, whole module (Go has no branch
     coverage);
   - per function: cognitive complexity **≤ 25**, **≤ 200** lines, nesting
     **≤ 4**, **≤ 5** parameters;
   - **≤ 500** lines per file (Markdown and `go.sum` excepted);
   - the layer rules in `scripts/import-rules.tsv` (the compiler forbids
     cycles);
   - gofmt, go vet, golangci-lint, the docs check, and version pins that agree.

   Existing violations live in the shrink-only baselines
   (`scripts/*-baseline.tsv`, all empty); a `//nolint` counts as one.
4. **Hard stops.** Stop and ask the operator for:
   - the protocol's classes: product, data, security, contract, gates,
     outward or costly, drift, objectives, review, environment, repo rule,
     permission
     (defined in `plugins/engine/skills/build/references/hard-stops.md`);
   - **loosening a gate:** a lower threshold, a baseline row, a `//nolint`, a
     linter exclusion, a bigger word budget, or an `exempt`, `ephemeral`,
     `budget-only` or `set` row added or widened in `scripts/doc-budgets.tsv`;
   - **a new runtime dependency:** any module in `go.mod` that the `engine`
     binary links. Dev tools (go.mod's `tool`
     block) are yours to add, logged as a ruling.
5. **One Go version.** `go.mod`'s `toolchain` line is the only source. Run
   every verb and lane through `./dev`, which pins it. Docs cite `go.mod`,
   never the version.
6. **Docs describe the current state.** No history, no "previously", no
   changelog. Docs and code never cite an issue, a PR, a task or a plan, nor
   any file in `design/`, which is ephemeral. Commit messages and PR bodies
   may.

## Always load

Read these at each session start:

- `docs/project-brief.md`: what engine is, its state, scope and stack.
- `docs/architecture.md`: components, layers and binding decisions.
- `docs/project-structure.md`: where everything lives and where new code goes.
- `docs/testing-strategy.md`: which test layer a claim belongs in.
- `docs/quality-gates.md`: lanes, targets, baselines, doc rules, setup, failures.

## Load on demand

| If your task… | Load |
| --- | --- |
| changes how agents plan, build or review | `plugins/engine/skills/build/SKILL.md`, `plugins/engine/skills/engine/SKILL.md` and their references |
| changes phases, roles, states or protocol principles | `docs/execution-protocol.md` |
| changes dispatch, recovery, merges, the merge queue, host ops or session signals | `docs/coordinator.md` |
| changes server records, API, credentials, inbox or `engine ui` | `docs/control-server.md` |
| changes host config, placement, attempts or session access | `docs/hosts-and-sessions.md` |
| changes a harness adapter or what the core asks of one | `docs/harnesses.md` |
| changes what is recorded about runs or where it is stored | `docs/telemetry.md` |
| sets up GitHub access, notifications, harness sign-in or a host | `docs/operator-setup.md` |
| changes what a target repo must provide, or the shape of this file or the doc rules | `docs/project-context.md` |
| changes a Go, tool or Action version, or handles a Dependabot PR | `docs/versions.md` |
| decides something not yet settled | the drafts in `design/`; never cite them |

## Docs

- **The bar.** A persistent doc holds only what (1) the code an agent loads
  anyway does not make obvious; (2) spans code locations an agent is
  unlikely to load, typically to keep product or engineering behaviour
  consistent; or (3) is a practice to follow. Cut the rest.
- **Header.** Every doc opens with one line, `> **Owns:** … **Not here:** …`.
- **Every link is a load trigger** ("if you are changing X, load Y"), never
  an unconditional "see also".
- **One place per fact.** Cite a registry that is the source
  (`scripts/lanes.tsv`, `scripts/import-rules.tsv`, `scripts/doc-budgets.tsv`,
  `go.mod`); never restate it.
- **Budgets.** Every doc has a word budget in `scripts/doc-budgets.tsv`;
  this file plus the always-load docs share about 4000 words. Over budget:
  split behind a load trigger, or cut what fails the bar.
- **Keep the docs true.** A change to layout, layers, tests, gates or
  versions updates its owning doc in the same PR. A new persistent doc gets
  a row above.

`./dev test docs-check` enforces these, except the bar, link wording, one
place per fact and keeping docs true: those are yours and the reviewer's.

## Workflow: worktree to merge

Build a task with the `build` skill (`plugins/engine/skills/build/SKILL.md`).
This repo sets the default mode: **hands-off**. A build that names no mode
merges on green without asking. Its repo specifics:

- **Worktree:** `./dev worktree create <type>/<topic>` from the main checkout;
  `./dev worktree release <branch>` at cleanup.
- **Gate:** `./dev test`.
- **Reviewer checklist:** this file.
- **PR body:** `.github/pull_request_template.md`.
- **Merge:** squash, pinned to the reviewed head.
- **CI failure:** read the run's `test-ai-results` artifact.
- **Attribution:** commits and reviews name the actual harness and model.
