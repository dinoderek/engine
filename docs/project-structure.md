# Project structure

> **Owns:** where everything lives and where new code, tests, lanes and docs go. **Not here:** why the layers are what they are (`architecture.md`).

## Layout

| Path | Holds |
| --- | --- |
| `cmd/engine/` | the `engine` main package: one line that calls `internal/cli` |
| `internal/` | product packages; `internal/cli` dispatches commands |
| `internal/attach/` | `engine attach` |
| `internal/hosts/` | host config: parsing, validation, slots and launch lines |
| `internal/hostops/` | `engine host <op> --json`: the ops a host runs on itself for a caller, and the preflight check |
| `internal/remote/` | the client that reaches a host's ops: `Local` is this host, in-process; `SSH` is another machine, and its tmux-over-SSH runner |
| `internal/session/` | the local host's ops: clones, worktrees, tmux windows, keeping a Mac awake, live Claude sessions, folder trust, the run directory |
| `internal/setup/` | `engine setup`: the seven steps that bring a host to the caller's engine commit, plugin and config |
| `internal/lint/` | `engine lint`: the plan rules |
| `internal/run/` | `engine run`: the coordinator loop (`coordinator.md`) |
| `internal/ps/` | `engine ps` |
| `internal/start/` | `engine start` |
| `internal/tell/` | `engine tell` |
| `devtools/cmd/dev/` | the `dev` main package |
| `devtools/internal/` | the `./dev` verbs, lane runner, lane checks, baselines and git and file helpers |
| `dev` | the shell entrypoint to `devtools/cmd/dev/` |
| `scripts/` | registries: lanes, import rules, doc budgets and baselines |
| `docs/` | the persistent docs |
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
- **A skill:** `plugins/engine/skills/<name>/SKILL.md`, with any references
  in `references/` beside it, and a budget row in `scripts/doc-budgets.tsv`
  for each file. Bump `version` in the plugin's `plugin.json`:
  Claude Code sees an update only when it changes.
- **A doc:** a file in `docs/` with the Owns/Not here header, a row in
  `AGENTS.md`'s load-on-demand table, and, if it needs one, a budget in
  `scripts/doc-budgets.tsv`.
