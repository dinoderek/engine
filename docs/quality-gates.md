# Quality gates

> **Owns:** the gate lanes' intent, the quality targets, baselines, the doc rules, setup, and debugging a failed gate. **Not here:** the lane list and commands (`scripts/lanes.tsv`), why the layers are what they are (`architecture.md`), where versions are pinned (`versions.md`).

## Run the gates

`./dev test` runs every required lane in parallel, with no fail-fast;
`./dev test <lane>...` runs named lanes, `./dev test for <path>...` those
paths' lanes.

Every `required` lane in `scripts/lanes.tsv` must be green before a PR. CI
runs `./dev test` on every pull request and every push to `main`, and uploads
`.test-ai-results/` as the `test-ai-results` artifact; no lane is local-only.
CI installs the tools whose tests skip without them and requires a Chromium
browser.

## What each lane enforces

| Lane | Enforces | Configured in |
| --- | --- | --- |
| `format` | `gofmt` layout, with the pinned toolchain | — |
| `vet` | `go vet` | — |
| `lint` | golangci-lint's standard linters plus the per-function targets below | `.golangci.yml`, `scripts/lint-baseline.tsv` |
| `unit` | the whole test suite passes under the race detector | — |
| `coverage` | **≥ 80% of statements**, whole module | `scripts/lanes.tsv` (`-min`) |
| `file-length` | **≤ 500 lines** per text file, except Markdown and `go.sum` | `scripts/lanes.tsv` (`-max`), `scripts/file-length-baseline.tsv` |
| `imports` | the layer rules | `scripts/import-rules.tsv`, `scripts/import-baseline.tsv` |
| `versions` | the version pins agree | `docs/versions.md` says what it checks |
| `docs-check` | the doc rules below | `scripts/doc-budgets.tsv` |
| `harness-names` | no string literal or identifier names a harness outside the adapters | `scripts/lanes.tsv` (flags) |

Per function (`lint`): cognitive complexity **≤ 25**, **≤ 200** lines,
control nesting **≤ 4** and **≤ 5** parameters. Every finding counts,
including several on one line.

Go has no branch coverage, so the target is statements, with
`-coverpkg=./...`: code counts as covered when any package's tests run it.

`imports` checks only layers (the compiler rejects cycles), on non-test
imports of files built on the host OS. A rule whose importer or target matches no package fails the lane.
Load `architecture.md` before changing a rule.

## Baselines only shrink

Each baseline row tolerates up to its count. Above the count the lane fails;
below it the lane also fails until you lower or delete the row. All baselines
are empty. A `//nolint` directive, in any form, is a lint finding and needs a
baseline row.

Adding a row, raising a count, lowering a threshold or excluding a linter is a
hard stop: it needs the operator's agreement, stated in the PR's
**Deviations**.

## Doc rules

`docs-check` treats every tracked Markdown file as a persistent doc, except
under the `exempt`, `ephemeral` and `budget-only` prefixes in
`scripts/doc-budgets.tsv`. `.github/` is exempt (no header, budget or index
row), `design/` ephemeral (no rule applies), and `plugins/` budget-only: a
skill is not a doc and cites paths in the repos that use it, so it keeps rules
1 and 6 only. It enforces:

1. **Word budgets** (whitespace-separated words): each doc's own or the
   default. A `set` row caps docs loaded together; the `always-load` set must
   hold exactly `AGENTS.md` and the docs under its "Always load" heading.
2. **Links resolve and cited paths exist.** Every relative link, and every
   code-span word naming a repository path, must exist relative to the doc or
   the root, outside fenced blocks. Gitignored outputs count. Cite a file,
   never a line.
3. **The header.** The first lines carry exactly one
   `> **Owns:** … **Not here:** …` line, with no load trigger.
4. **The index is complete.** Every doc except the entrypoints (`AGENTS.md`
   and `README.md`) appears in `AGENTS.md`'s "Always load" or "Load on
   demand". `CLAUDE.md` is a symlink to `AGENTS.md`.
5. **Lane citations are real.** Every `./dev test <lane>` names a lane.
6. **No tracker or ephemeral references** in any file outside an ephemeral
   directory, `.github/` included: no issue or PR number or URL, no plan file
   path, and no path to a file in an ephemeral directory. The bare directory
   name is fine, and so are placeholders such as `#<task>`.

The check does not judge link wording, one place per fact, or whether a doc
is still true; the reviewer does.

## Setup and debugging

`./dev doctor` installs what Homebrew can and reports any other gap with its
fix, and installs Go with Homebrew when none is on `PATH`; `./dev` sets `GOTOOLCHAIN` from go.mod, so any Go can bootstrap. For a failed gate, read
`.test-ai-results/summary.md` (one PASS or FAIL line per lane), then
`.test-ai-results/<lane>.txt`, which starts with the command and exit code,
and rerun it with `./dev test <lane>`.
