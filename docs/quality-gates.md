# Quality gates

> **Owns:** the gate lanes' intent, the quality targets, import rules, baselines, setup, and debugging a failed gate. **Not here:** the lane list and commands (`scripts/lanes.tsv`), where versions are pinned (`versions.md`).

## Run the gates

```bash
./dev test                  # every required lane, in parallel, no fail-fast
./dev test <lane>...        # named lanes, e.g. ./dev test unit coverage
./dev test for <path>...    # the required lanes those paths trigger
./dev test --list           # the lane registry
```

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
| `file-length` | **≤ 500 lines** per text file; Markdown has word budgets instead | `scripts/lanes.tsv` (`-max`), `scripts/file-length-baseline.tsv` |
| `imports` | the layer rules | `scripts/import-rules.tsv`, `scripts/import-baseline.tsv` |
| `versions` | the version pins agree | `docs/versions.md` says what it checks |

Per function (`lint`): cognitive complexity **≤ 25** (gocognit), **≤ 200**
lines (funlen), control nesting **≤ 4** and **≤ 5** parameters (revive). Every
finding counts, including several on one line.

**Go has no branch coverage.** Its cover tool counts statements, so the
coverage target is statements only. Tests run with `-coverpkg=./...`, so code
counts as covered when any package's tests run it.

## Import rules

The Go compiler rejects import cycles, so `imports` checks only the layers in
`scripts/import-rules.tsv`, on non-test imports. Test files may import anything.

- **Product code** is `cmd/engine` and `internal/`. It never imports `devtools/`.
  `cmd/engine` only parses arguments through `internal/cli`.
- **Repo tooling** is `devtools/`: the `./dev` verbs and the lane checks. It
  never imports product code, so the gates cannot depend on what they check.
  Go's `internal/` rule also keeps `devtools/internal/` out of product reach.

Add a package by adding its rule in the same PR. A rule that matches no
package fails the lane.

## Baselines only shrink

Violations that existed when a gate landed are recorded in the baseline files
named above. All are empty. Each row tolerates up to its count. Above the count
the lane fails; below it the lane also fails until you lower or delete the row,
so a baseline only shrinks. A `//nolint` directive counts as a lint finding
(linter `nolint`), so it needs a baseline row too.

Adding a row, raising a count, lowering a threshold or excluding a linter is a
hard stop: it needs the operator's agreement, stated in the PR's
**Deviations**. Meet a target by adding tests, splitting the function or
moving the code.

## Setup

Run `./dev doctor`. It installs Go with Homebrew if no Go is on `PATH`, then
checks git, gh, the pinned toolchain, a C compiler for the race detector,
the modules, and the go.mod tools (golangci-lint, govulncheck), installing
what Homebrew can. Anything else it reports as a gap with its fix. Any Go
release can bootstrap: `./dev` sets `GOTOOLCHAIN` from go.mod, and Go downloads
that toolchain.

## Debugging a failed gate

1. Read `.test-ai-results/summary.md`: one PASS or FAIL line per lane.
2. Read that lane's `.test-ai-results/<lane>.txt`. It is written on every
   run, and starts with the command and exit code.
3. Rerun just that lane: `./dev test <lane>`.
4. `format`: run `gofmt -w` on the files it names. `coverage`: the lane lists
   every file below the target.
