# Project structure

> **Owns:** where everything lives and where new code, tests, lanes and docs go. **Not here:** why the layers are what they are (`architecture.md`).

## Layout

| Path | Holds |
| --- | --- |
| `cmd/engine/` | the `engine` main package: one line that calls `internal/cli` |
| `internal/` | product packages; `internal/cli` dispatches commands |
| `devtools/cmd/dev/` | the `dev` main package that `./dev` builds and runs |
| `devtools/internal/` | the `./dev` verbs (`dev`), the lane runner (`lanes`), the lane checks (`checks`), baselines (`baseline`) and git and file helpers (`repo`) |
| `dev` | the shell entrypoint: pins Go from `go.mod`, builds `.dev/dev`, runs it |
| `scripts/` | registries: lanes, import rules, doc budgets, and the three shrink-only baselines |
| `docs/` | the persistent docs, every one indexed in `AGENTS.md` |
| `design/` | ephemeral drafts: exempt from the doc rules, never cited |
| `plugins/engine/` | the `engine` Claude Code plugin: its skills in `skills/<name>/SKILL.md`, its hooks in `plugins/engine/hooks/hooks.json` |
| `.claude-plugin/` | the marketplace that publishes `plugins/` |
| `.claude/` | this repo's Claude Code settings: it enables the `engine` plugin |
| `.github/` | the CI workflow, Dependabot config, PR template and task-issue template |
| `go.mod`, `go.sum` | the module, the Go version, and the dev tools |
| `.golangci.yml` | the lint configuration |

`.dev/` and `.test-ai-results/` are gitignored outputs.

## Where new things go

- **A product feature:** a new package under `internal/<name>/`, wired into
  `internal/cli`. Add its row to `scripts/import-rules.tsv`.
- **An `engine` command:** a case in `internal/cli` that calls the package
  that does the work. The command's logic never lives in `internal/cli`.
- **Tests:** a `_test.go` file beside the code, in the same package. Fixtures
  go in that package's `testdata/`.
- **A gate lane:** a row in `scripts/lanes.tsv`. If no single command does
  the job, add a `dev check` subcommand in `devtools/internal/checks`, then a
  row in the lane table of `quality-gates.md`.
- **A `./dev` verb:** a method on `dev.Tool` in `devtools/internal/dev`, plus
  its line in the usage text.
- **A skill:** `plugins/engine/skills/<name>/SKILL.md`, with a budget row in
  `scripts/doc-budgets.tsv`. Bump `version` in the plugin's `plugin.json`:
  Claude Code sees an update only when it changes.
- **A doc:** a file in `docs/` with the Owns/Not here header, a row in
  `AGENTS.md`'s load-on-demand table, and, if it needs one, a budget in
  `scripts/doc-budgets.tsv`.
