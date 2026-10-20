# AGENTS.md

> **Owns:** the rules every session in this repo follows, and the index of every doc. **Not here:** the protocol a task follows (`docs/execution-protocol.md`), how each gate works (`docs/quality-gates.md`).

The single agent entrypoint; `CLAUDE.md` is a symlink to it. The rules are in
this file on purpose: **links are for depth, never for rules.**

## Non-negotiables

1. **Every gate is mandatory.** Get `./dev test` green before every PR; CI
   runs the same command. A lane that cannot run here is a setup gap: run
   `./dev doctor` and fix it, never skip the lane. Never report a lane you did
   not run as passed, or fake any evidence.
2. **Tests always.** Every code change adds or updates tests for the
   behaviour it changes, in the layer `docs/testing-strategy.md` assigns. A
   gate check gets a test that proves it fails on a violation.
3. **Quality targets are gates, not advice.** `./dev test` enforces:
   - coverage **≥ 80% of statements**, whole module (Go has no branch
     coverage);
   - per function: cognitive complexity **≤ 25**, **≤ 200** lines, nesting
     **≤ 4**, **≤ 5** parameters;
   - **≤ 500** lines per file;
   - the layer rules in `scripts/import-rules.tsv` (the compiler forbids
     cycles);
   - gofmt, go vet, golangci-lint, the docs check, and version pins that agree.

   Existing violations live in the baselines (`scripts/*-baseline.tsv`, all
   empty), which only shrink. A `//nolint` directive counts as a violation.
   Meet a target by adding tests, splitting the function or moving the code.
4. **Hard stops.** Stop and ask the operator for:
   - the protocol's classes: product, data, security, contract, gates,
     outward or costly, drift, objectives, review, environment
     (`docs/execution-protocol.md` defines each);
   - **loosening a gate:** a lower threshold, a baseline row, a `//nolint`, a
     linter exclusion or a bigger word budget;
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
- `docs/architecture.md`: components, layers and the decisions new code must respect.
- `docs/project-structure.md`: where everything lives and where new code goes.
- `docs/testing-strategy.md`: which test layer a claim belongs in.
- `docs/quality-gates.md`: the lanes, targets, baselines, doc rules, setup and fixing a failed gate.

## Load on demand

| If your task… | Load |
| --- | --- |
| changes phases, roles, task states, rulings, hard stops or merge rules, or executes a task card | `docs/execution-protocol.md` |
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
- **Every link is a load trigger.** A link says when to follow it ("if you
  are changing X, load Y"). Never an unconditional "see also".
- **One place per fact.** When a registry is the source (`scripts/lanes.tsv`,
  `scripts/import-rules.tsv`, `scripts/doc-budgets.tsv`, `go.mod`), cite it;
  never restate it.
- **Budgets.** Every doc has a word budget in `scripts/doc-budgets.tsv`;
  this file plus the always-load docs share about 4000 words. Over budget:
  split behind a load trigger, or trim what repeats another doc or the code.
- **No orphans.** A new persistent doc gets a row above, in the same PR.
- **Keep the docs true.** A change to layout, layers, tests, gates or
  versions updates the owning doc in the same PR.

`./dev test docs-check` enforces all of these except link wording.
`design/` is exempt from every doc rule.

## Workflow: worktree to merge

1. **Worktree.** From the main checkout, `./dev worktree create <type>/<topic>`
   branches from the remote's default branch into a worktree outside the
   checkout; work only there. If your harness opened one, merge `origin/main`
   first. One task, one worktree, one branch, one PR.
   Commit work in progress; never stash (the stash is shared).
2. **Gates.** Build with tests and docs until `./dev test` is green. Before
   every push, fetch; if `origin/main` moved, merge it in and rerun.
3. **Fresh review.** Spawn a reviewer that did not write the diff, on the
   diff against `origin/main`, with this file as its checklist. Its first line
   is `Verdict: APPROVED` or `Verdict: CHANGES_REQUESTED`. Fix every real
   finding, rerun the gates, and review again. List declined findings in the
   PR with the reason.
4. **PR** per `.github/pull_request_template.md`: gate evidence, rulings,
   deviations.
5. **CI.** Watch with `gh pr checks --watch` until green. On a failure, read
   the run's `test-ai-results` artifact, fix it and go back to step 2.
6. **Merge.** Hands-off: merge on green with
   `gh pr merge --squash --match-head-commit <sha>`, where `<sha>` is the
   commit that was reviewed and gated. Gated: say the PR is ready and wait;
   never merge or enable auto-merge without the operator's approval.
7. **Cleanup.** `./dev worktree release <branch>`, then delete the remote
   branch.

Commits and reviews carry truthful attribution: the harness and the actual
model. Never invent an identity.
