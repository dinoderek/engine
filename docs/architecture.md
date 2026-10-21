# Architecture

> **Owns:** the components, the layers and their import rules, and the decisions new code must respect. **Not here:** each component's design (the load-on-demand docs in `AGENTS.md`), file locations (`project-structure.md`).

## Components

Two programs share one Go module:

- **`engine`**, the product: `cmd/engine` → `internal/cli` → one package per
  command. `internal/cli` parses arguments and dispatches. Today:
  `internal/version` (`engine version`), `internal/tracker` (the GitHub
  tracker adapter, through `gh`: `engine labels sync`, and reads of a task
  issue and of the claim comments on every claimed task) and `internal/hook`
  (harness hooks: `engine hook pre-tool-use`).
- **`dev`**, the repo's tooling behind `./dev`: `devtools/cmd/dev` →
  `devtools/internal/dev` (the verbs) → `lanes` (the registry and parallel
  runner) and `checks` (the lanes that are not one external command) →
  `baseline` and `repo` (git, go and file helpers).

The designed components are the coordinator's reconcile loop, the host ops
(`engine host <op> --json`), the rest of the tracker adapter and the
telemetry collector.
If you are building one, load its doc from the index in `AGENTS.md`.

## Layers and import rules

`scripts/import-rules.tsv` is the source; the `imports` lane enforces it on
non-test imports. The reasons:

- **Product never imports tooling, and tooling never imports product.** So
  the gates cannot depend on the code they check, and `engine` ships nothing
  from `devtools/`. Go's `internal/` rule also keeps `devtools/internal/` out
  of the product's reach.
- **Each package may import only what its rule lists.** `cmd/engine` sees only
  `internal/cli`. Leaf packages (`internal/version`, `internal/tracker`,
  `internal/hook`, `devtools/internal/repo`) import no first-party code. A new package adds its rule in the same PR.

## Decisions new code must respect

- **One binary, `engine`, for macOS and Linux.** The same binary runs on every
  host.
- **Standard library only at runtime.** A new runtime dependency is a hard
  stop (`AGENTS.md`).
- **Outside systems only through their CLIs** (`git`, `gh`, `tmux`, `ssh`, the
  harnesses), behind an injectable runner such as `repo.Runner`, so tests
  replace them.
- **The coordinator is stateless.** It derives everything from the tracker
  and the hosts on each pass, and never edits code or merges. If you are
  changing that, load `coordinator.md`.
- **The reconcile core is a package with no CLI assumptions**, so a daemon can
  reuse it later. `internal/cli` stays a thin dispatcher.
- **Tooling is Go too.** The repo has one toolchain. A lane is either one
  external command or a `dev check` subcommand, tested like product code.
