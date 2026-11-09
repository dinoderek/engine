# Architecture

> **Owns:** the components, the layers and their import rules, and the decisions new code must respect. **Not here:** each component's design (the load-on-demand docs in `AGENTS.md`), file locations (`project-structure.md`).

## Components

Two programs share one Go module:

- **`engine`**, the product: `cmd/engine` → `internal/cli` → one package per
  command. `internal/cli` parses arguments and dispatches. `internal/control`
  owns server records, transactions and HTTP; `internal/reconcile` decides
  each server pass from a View of records and host observations;
  `internal/controlrun` builds the View, applies its actions and caches host observations; `internal/controlcmd` supplies the CLI
  and daemon lifecycle. The CLI and coordinator read a plan's tasks through
  `internal/controlrun` and validate them with `internal/lint`.
  Other packages:
  `internal/version` (`engine version`), `internal/hook`
  (harness hooks: `engine hook pre-tool-use`, `permission` and `state`),
  `internal/start` (`engine start --check`: the preflight session on a host),
  `internal/hostops` (`engine host <op> --json`: the ops a host runs on
  itself for a caller), `internal/remote` (the client that reaches a host's
  ops: `Local` calls them in-process, `SSH` over `ssh`),
  `internal/lint` (`engine lint`: a plan's rules), `internal/ui`
  (`engine ui`: the loopback dashboard, which holds the operator credential and
  forwards only fixed control reads). The commands
  share `internal/hosts`, the host config: it parses `hosts.toml` with an
  in-house reader of the TOML subset the config uses, validates it, works out
  each machine's slots and expands launch lines. `internal/session` is the
  host's session ops, through `git`, `gh` and `tmux`: engine's clones and task
  worktrees, the tmux session, windows and pane logs, the run record and the
  harness's state file. `internal/harness` is what engine asks of a harness:
  the `Adapter` interface (launch, observe, check, release). Each harness is an
  adapter package below it (`claude`, `codex`), and `internal/harness/registry`
  lists them. The commands build their adapters from the registry and hand
  them to `internal/hostops`, which sees only the interface.
- **`dev`**, the repo's tooling behind `./dev`: `devtools/cmd/dev` →
  `devtools/internal/dev` (the verbs) → `lanes` (the registry and parallel
  runner) and `checks` (the lanes that are not one external command) →
  `baseline` and `repo` (git, go and file helpers).

## Layers and import rules

`scripts/import-rules.tsv` is the source; the `imports` lane enforces it on
non-test imports. The reasons:

- **Product never imports tooling, and tooling never imports product.** So
  the gates cannot depend on the code they check, and `engine` ships nothing
  from `devtools/`. Go's `internal/` rule also keeps `devtools/internal/` out
  of the product's reach.
- **Each package may import only what its rule lists.** `cmd/engine` sees only
  `internal/cli`. Leaf packages (`internal/version`,
  `internal/hook`, `internal/hosts`, `internal/session`,
  `devtools/internal/repo`) import no first-party code; a command package
  such as `internal/start` composes them. Only the registry imports a
  concrete harness adapter. A new package adds its rule in the
  same PR.

## Decisions new code must respect

- **One binary, `engine`, for macOS and Linux.** The same binary runs on every
  host.
- **Standard library only at runtime.** A new runtime dependency is a hard
  stop (`AGENTS.md`).
- **External tools use injectable CLI runners**, including `sqlite3`.
  Engine's own server exposes a versioned HTTP API.
- **Coordination derives decisions from authoritative records and host
  observations.** The server persists intent and execution identities.
  Coordinators never edit code; the server's coordinator merges its tasks.
  For server records and API, load `control-server.md`; for dispatch, merges,
  host reach and session signals, load `coordinator.md`.
- **The reconcile core has no CLI assumptions.** `internal/reconcile` reads
  only its View. `internal/cli` stays a thin dispatcher.
- **Tooling is Go too.** The repo has one toolchain. A lane is either one
  external command or a `dev check` subcommand, tested like product code.
