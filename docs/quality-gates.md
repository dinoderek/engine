# Quality gates

> **Owns:** the gate lanes' intent, the quality targets, baselines, the doc rules, setup, and debugging a failed gate. **Not here:** the lane list and commands (`scripts/lanes.tsv`), why the layers are what they are (`architecture.md`), where versions are pinned (`versions.md`).

## Run the gates

```bash
./dev test                  # every required lane, in parallel, no fail-fast
./dev test <lane>...        # named lanes, e.g. ./dev test unit coverage
./dev test for <path>...    # the required lanes those paths trigger
./dev test --list           # the lane registry
```

`./dev help` lists every verb. There is no `deploy` verb: engine has nothing
to deploy yet.

Every `required` lane in `scripts/lanes.tsv` must be green before a PR. CI
(`.github/workflows/ci.yml`) runs `./dev test` on every pull request and every
push to `main`, and uploads `.test-ai-results/` as the `test-ai-results`
artifact. Every lane runs in CI; none is local-only.

## What each lane enforces

| Lane | Enforces | Configured in |
| --- | --- | --- |
| `format` | `gofmt` layout, using the pinned toolchain's gofmt | — |
| `vet` | `go vet` | — |
| `lint` | golangci-lint's standard linters plus the per-function targets below | `.golangci.yml`, `scripts/lint-baseline.tsv` |
| `unit` | the whole test suite passes under the race detector | — |
| `coverage` | **≥ 80% of statements**, whole module | `scripts/lanes.tsv` (`-min`) |
| `file-length` | **≤ 500 lines** per text file, except Markdown and `go.sum` | `scripts/lanes.tsv` (`-max`), `scripts/file-length-baseline.tsv` |
| `imports` | the layer rules | `scripts/import-rules.tsv`, `scripts/import-baseline.tsv` |
| `versions` | the version pins agree | `docs/versions.md` says what it checks |
| `docs-check` | the doc rules below | `scripts/doc-budgets.tsv` |

Per function (`lint`): cognitive complexity **≤ 25** (gocognit), **≤ 200**
lines (funlen), control nesting **≤ 4** and **≤ 5** parameters (revive). Every
finding counts, including several on one line.

**Go has no branch coverage.** Its cover tool counts statements, so the
coverage target is statements only. Tests run with `-coverpkg=./...`, so code
counts as covered when any package's tests run it.

## Import rules

The Go compiler rejects import cycles, so `imports` checks only the layers in
`scripts/import-rules.tsv`, on non-test imports. Test files may import
anything. It sees the files built for the host OS, so CI on Linux also covers
any Linux-only file. A rule whose importer or a target matches no package
fails the lane. If you are adding a package or changing a rule, load
`architecture.md`, which owns the reasons.

## Baselines only shrink

Violations that existed when a gate landed are recorded in the baseline files
named above. All are empty. Each row tolerates up to its count. Above the count
the lane fails; below it the lane also fails until you lower or delete the row,
so a baseline only shrinks. A `//nolint` directive, in any form golangci-lint
honours, counts as a lint finding (linter `nolint`), so it needs a baseline row
too.

Adding a row, raising a count, lowering a threshold or excluding a linter is a
hard stop: it needs the operator's agreement, stated in the PR's
**Deviations**. Meet a target by adding tests, splitting the function or
moving the code.

## Doc rules

`docs-check` treats every tracked Markdown file as a persistent doc, except
under the `exempt` and `ephemeral` prefixes in `scripts/doc-budgets.tsv`.
`design/` is ephemeral: drafts there follow no doc rule. It enforces:

1. **Word budgets** (whitespace-separated words). Each doc has its own budget
   or the default. A `set` row caps docs loaded together. The `always-load`
   set must hold exactly `AGENTS.md` and the docs listed under its "Always
   load" heading. The lane output lists every count against its budget.
2. **Links resolve and cited paths exist.** Every relative link, and every
   code-span word that names a repository path, must exist, relative to the
   doc or to the root. Gitignored outputs count as existing. Cite a file,
   never a line: line numbers rot.
3. **The header.** The first lines carry exactly one
   `> **Owns:** … **Not here:** …` line, with no load trigger.
4. **The index is complete.** Every doc except the `unindexed` ones (the
   entrypoints) appears under "Always load" or "Load on demand" in
   `AGENTS.md`. `CLAUDE.md` is a symlink to `AGENTS.md`.
5. **Lane citations are real.** Every `./dev test <lane>` names a lane.
6. **No tracker or ephemeral references**, in docs or code: no issue or PR
   number (alone or after `owner/repo`), no issue or PR URL, no plan file
   path, and no path to a file in an ephemeral directory. The bare directory
   name is fine.

## Setup

Run `./dev doctor`. It installs Go with Homebrew if no Go is on `PATH`, then
checks git, gh, the pinned toolchain, a C compiler for the race detector,
the modules, and the go.mod tools (golangci-lint, govulncheck), installing
what Homebrew can. Anything else it reports as a gap with its fix. Any Go
release can bootstrap: `./dev` sets `GOTOOLCHAIN` from go.mod, and Go downloads
that toolchain.

## Debugging a failed gate

1. Read `.test-ai-results/summary.md`: one PASS or FAIL line per lane of the
   last run. Each run replaces the directory.
2. Read that lane's `.test-ai-results/<lane>.txt`. It starts with the command
   and exit code.
3. Rerun just that lane: `./dev test <lane>`.
4. `format`: run `gofmt -w` on the files it names. `coverage`: the lane lists
   every file below the target.
