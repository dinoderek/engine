# Project structure

> **Owns:** where everything lives and where new code, tests, lanes and docs go. **Not here:** why the layers are what they are (`architecture.md`).

## Layout

| Path | Holds |
| --- | --- |
| `cmd/engine/`, `internal/` | the entrypoint and product packages, one per component or command (`internal/cli` dispatches) |
| `internal/hostops/`, `internal/remote/` | a host's own ops (`engine host <op> --json`), and their client: `Local` in-process, `SSH` to another machine |
| `internal/harness/` | the `Adapter` interface and one package per harness; `registry/` is the only importer of an adapter; `harnesstest/` holds the adapter contract and a fake |
| `devtools/cmd/dev/`, `devtools/internal/`, `dev` | `./dev`: the main package; the verbs, lane runner, lane checks, baselines and helpers; the shell entrypoint |
| `scripts/` | registries: lanes, import rules, doc budgets and baselines |
| `docs/`, `design/` | the persistent docs; ephemeral drafts, never cited |
| `plugins/engine/` | the plugin: skills in `skills/<name>/SKILL.md`, Claude hooks in `plugins/engine/hooks/hooks.json`, Codex manifest and hooks in `plugins/engine/.codex-plugin/plugin.json` |
| `.claude-plugin/`, `.agents/plugins/` | the Claude and Codex marketplaces that publish `plugins/` |

`.dev/` and `.test-ai-results/` are gitignored outputs.

## Where new things go

- **A product feature:** a new package under `internal/<name>/`, wired into
  `internal/cli`, with its row in `scripts/import-rules.tsv`. A command's
  logic never lives in `internal/cli`.
- **Tests:** a `_test.go` file beside the code; fixtures in `testdata/`.
- **A gate lane:** a row in `scripts/lanes.tsv`. If no single command does
  the job, add a `dev check` subcommand in `devtools/internal/checks`, then a
  row in the lane table of `quality-gates.md`.
- **A `./dev` verb:** a method on `dev.Tool` in `devtools/internal/dev`, plus
  its line in the usage text.
- **A skill:** `plugins/engine/skills/<name>/SKILL.md`, references in
  `references/`, and a budget row in `scripts/doc-budgets.tsv` per file. Bump
  `version` in both of the plugin's `plugin.json` files: the harnesses see an
  update only when it changes.
- **A doc:** a file in `docs/` with the Owns/Not here header, a row in
  `AGENTS.md`'s load-on-demand table, and a budget in
  `scripts/doc-budgets.tsv` if it needs one.
