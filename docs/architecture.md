# Architecture

> **Owns:** the components, the layers and their import rules, and the decisions new code must respect. **Not here:** each component's design (the load-on-demand docs in `AGENTS.md`), file locations (`project-structure.md`).

## Components

Two programs share one Go module:

- **`engine`**, the product: `cmd/engine` → `internal/cli` → one package per
  command. `internal/cli` parses arguments and dispatches. `internal/control`
  owns server records, transactions and HTTP; `internal/controlrun` adapts
  reconcile decisions to server state and caches host observations; `internal/controlcmd` supplies the CLI
  and daemon lifecycle. The CLI and coordinator share the read-only plan
  projection in `internal/controlrun` and validate it with `internal/lint`.
  Other packages:
  `internal/version` (`engine version`), `internal/tracker` (the GitHub
  tracker adapter, through `gh`: `engine labels sync`, and reads
  of a task issue and of the claim comments on every claimed task, and
  posting a comment),
  `internal/hook`
  (harness hooks: `engine hook pre-tool-use`, `permission` and `state`), `internal/attach`
  (`engine attach`: a task's claim, then its tmux window), `internal/ps`
  (`engine ps --github`: the state of each claimed task), `internal/start`
  (`engine start`: checks a task, then has its host launch the session),
  `internal/hostops` (`engine host <op> --json`: the ops a host runs on
  itself for a caller), `internal/remote` (the client that reaches a host's
  ops: `Local` calls them in-process, `SSH` over `ssh`), `internal/tell`
  (`engine tell`: comments on a task and points its session at it),
  `internal/lint` (`engine lint`: a feature's plan rules), `internal/run`
  (`engine run`: the coordinator's loop, on top of the others), `internal/ui`
  (`engine ui`: the loopback dashboard, which holds the operator credential and
  forwards only fixed control reads). The commands
  share `internal/hosts`, the host config: it parses `hosts.toml` with an
  in-house reader of the TOML subset the config uses, validates it, works out
  each machine's slots and expands launch lines. `internal/session` is the
  host's session ops, through `git`, `gh`, `tmux` and the harness: engine's
  clones and task worktrees, the tmux session, windows and pane logs, the live
  Claude Code sessions, Claude Code's folder trust, engine's Codex profiles, the run record and the
  harness's state file.
- **`dev`**, the repo's tooling behind `./dev`: `devtools/cmd/dev` →
  `devtools/internal/dev` (the verbs) → `lanes` (the registry and parallel
  runner) and `checks` (the lanes that are not one external command) →
  `baseline` and `repo` (git, go and file helpers).

The telemetry collector is designed.
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
  `internal/hook`, `internal/hosts`, `internal/session`,
  `devtools/internal/repo`) import no first-party code; a command package
  such as `internal/start` composes them. A new package adds its rule in the
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
  Coordinators never edit code or merge. For server changes, load
  `control-server.md`; for GitHub-mode changes, load `coordinator.md`.
- **The reconcile core is a package with no CLI assumptions**, so a daemon can
  reuse it later. `internal/cli` stays a thin dispatcher.
- **Tooling is Go too.** The repo has one toolchain. A lane is either one
  external command or a `dev check` subcommand, tested like product code.
