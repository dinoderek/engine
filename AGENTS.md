# AGENTS.md

> **Owns:** the rules every session in this repo follows, and the index of every doc. **Not here:** the protocol a task follows (`docs/execution-protocol.md`), how each gate works (`docs/quality-gates.md`).

The single agent entrypoint; `CLAUDE.md` is a symlink to it. The rules are in
this file on purpose: **links are for depth, never for rules.**

## Non-negotiables

1. **Every gate is mandatory.** Get `./dev test` green before every PR; CI
   runs the same command. A lane that cannot run here is a setup gap: fix it
   with `./dev doctor`, never skip it. Never report a lane you did
   not run as passed, or fake any evidence.
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
   (`scripts/*-baseline.tsv`, all empty). A `//nolint` counts as a violation.
   Meet a target with tests, a split function or moved code.
4. **Hard stops.** Stop and ask the operator for:
   - the protocol's classes: product, data, security, contract, gates,
     outward or costly, drift, objectives, review, environment
     (the executor skill, `plugins/engine/skills/executor/SKILL.md`,
     defines each);
   - **loosening a gate:** a lower threshold, a baseline row, a `//nolint`, a
     linter exclusion, a bigger word budget, or an `exempt`, `ephemeral`,
     `budget-only` or `set` row added or widened in `scripts/doc-budgets.tsv`;
   - **a new runtime dependency:** any module in `go.mod` that the `engine`
     binary links. Dev tools (go.mod's `tool` block) are yours to add, logged
     as a ruling.
5. **One Go version.** `go.mod`'s `toolchain` line is the only source. Run
   every verb and lane through `./dev`, which pins it. Docs cite `go.mod`,
   never the version.
6. **Docs describe the current state.** No history, no "previously", no
   changelog. Docs and code never cite an issue, a PR, a task or a plan, nor
   any file in `design/`, which is ephemeral. Commit messages and PR bodies
   may.

## Always load

Read these at the start of every session:

- `docs/project-brief.md`: what engine is, its state, scope and stack.
- `docs/architecture.md`: components, layers and binding decisions.
- `docs/project-structure.md`: where everything lives and where new code goes.
- `docs/testing-strategy.md`: which test layer a claim belongs in.
- `docs/quality-gates.md`: lanes, targets, baselines, doc rules, setup, failures.

## Load on demand

| If your task… | Load |
| --- | --- |
| changes phases, roles, task states or the executor skill | `docs/execution-protocol.md` |
| changes how the coordinator runs, triggers, reaches hosts or identifies itself | `docs/coordinator.md` |
| changes host config, placement or session access | `docs/hosts-and-sessions.md` |
| changes what is recorded about runs or where it is stored | `docs/telemetry.md` |
| sets up engine's GitHub identity, tokens, notifications or harness sign-in | `docs/operator-setup.md` |
| changes what a target repo must provide, the seed prompt, or the shape of this file or the doc rules | `docs/project-context.md` |
| changes a Go, tool or Action version, or handles a Dependabot PR | `docs/versions.md` |
| decides something not yet settled | the drafts in `design/`; never cite them |

## Docs

- **Header.** Every doc opens with one line, `> **Owns:** … **Not here:** …`,
  so each fact lands in its owner. No load trigger: this index does that.
- **Every link is a load trigger** ("if you are changing X, load Y"), never
  an unconditional "see also".
- **One place per fact.** Cite a registry that is the source
  (`scripts/lanes.tsv`, `scripts/import-rules.tsv`, `scripts/doc-budgets.tsv`,
  `go.mod`); never restate it.
- **Budgets.** Every doc has a word budget in `scripts/doc-budgets.tsv`;
  this file plus the always-load docs share about 4000 words. Over budget:
  split behind a load trigger, or trim what repeats another doc or the code.
- **No orphans.** A new persistent doc gets a row above, in the same PR.
- **Keep the docs true.** A change to layout, layers, tests, gates or
  versions updates its owning doc in the same PR.

`./dev test docs-check` enforces these, except link wording, one place per
fact and keeping docs true: those are yours and the reviewer's. `design/` is
exempt from every doc rule.

## Workflow: worktree to merge

1. **Worktree.** From the main checkout, `./dev worktree create <type>/<topic>`
   branches from the remote's default branch into a worktree outside the
   checkout; work only there. If your harness opened one, merge `origin/main`
   first. One task, one worktree, one branch, one PR.
   Commit work in progress; never stash (the stash is shared).
2. **Gates.** Build with tests and docs until `./dev test` is green. Before
   every push, fetch; if `origin/main` moved, merge it in and rerun.
3. **Fresh review.** A reviewer that did not write the diff reviews it
   against `origin/main`, with this file as its checklist. Its first line is
   `Verdict: APPROVED` or `Verdict: CHANGES_REQUESTED`. Fix real findings,
   rerun the gates, review again. Declined findings go in the PR, with reasons.
4. **PR** per `.github/pull_request_template.md`: gate evidence, rulings,
   deviations.
5. **CI.** Watch with `gh pr checks --watch` until green. On a failure, read
   the run's `test-ai-results` artifact, fix, and return to step 2.
6. **Merge.** Hands-off: merge on green with
   `gh pr merge --squash --match-head-commit <sha>`, where `<sha>` is the
   commit that was reviewed and gated. Gated: say the PR is ready and wait;
   never merge or enable auto-merge without the operator's approval.
7. **Cleanup.** `./dev worktree release <branch>`.

Commits and reviews carry truthful attribution: the harness and the actual
model. Never invent an identity.
