# Architecture

> **Owns:** the components, the layers and their import rules, and the decisions new code must respect. **Not here:** each component's design (the load-on-demand docs in `AGENTS.md`), file locations (`project-structure.md`).

## Components

Two programs share one Go module:

- **`engine`**, the product: `cmd/engine` → `internal/cli` → one package per
  command. `internal/cli` only parses arguments and dispatches; a command's
  logic lives in its own package. The server side is `internal/control`
  (records, transactions, HTTP), `internal/reconcile` (each server pass,
  decided from a View of records and host observations),
  `internal/controlrun` (builds the View, applies its actions, caches host
  observations) and `internal/controlcmd` (the server's CLI and lifecycle).
  Hosts are reached through `internal/remote`, and each host's own ops run in
  `internal/hostops`. `internal/hosts` is the host config every command
  shares. `internal/session` runs a host's sessions through `git`, `gh` and
  `tmux`. A harness is the `internal/harness` `Adapter` interface; each
  harness is a package below it, listed by `internal/harness/registry`. The
  commands build the adapters and hand them to `internal/hostops`,
  `internal/setup` and `internal/upgrade`, which see only the interface.
  `internal/upgrade` is a fleet loop around `engine setup`, run as a
  subprocess of the new binary; it reaches the server only through a task
  list it is handed.
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
  `internal/cli`. Leaf packages (`internal/version`, `internal/hosts`,
  `internal/session`, `devtools/internal/repo`) import no first-party code, and
  `internal/hook` only `internal/harness`; a command package such as
  `internal/start` composes them. Only the registry imports a concrete harness
  adapter; the `harness-names` lane keeps harness names out of other code. A
  new package adds its rule in the same PR.

## Decisions new code must respect

- **One binary, `engine`, for macOS and Linux.** The same binary runs on every
  host.
- **The runtime is the standard library plus approved libraries:** `go.mod`'s
  direct requirements. Each has no requirements and no cgo, so the build stays
  cgo-free. A new one is a hard stop (`AGENTS.md`).
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
