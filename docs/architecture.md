# Architecture

> **Owns:** the components, the layers and their import rules, and the decisions new code must respect. **Not here:** each component's design (the load-on-demand docs in `AGENTS.md`), file locations (`project-structure.md`).

## Components

Two programs share one Go module:

- **`engine`**, the product: `cmd/engine` → `internal/cli` → one package per
  command. `internal/cli` only parses arguments and dispatches. The server
  side: `internal/control` holds records, transactions and HTTP;
  `internal/reconcile` decides each pass from a View of records and host
  observations; `internal/controlrun` builds the View and applies the
  actions. Hosts are reached through `internal/remote`, whose peer
  `internal/hostops` runs on the host. A harness is the `internal/harness`
  `Adapter` interface, one package per harness; `internal/hostops` and
  `internal/setup` see only the interface.
- **`dev`**, the repo's tooling behind `./dev`: `devtools/cmd/dev` →
  `devtools/internal/dev` (the verbs) → `lanes` and `checks` → `baseline` and
  `repo`.

## Layers and import rules

`scripts/import-rules.tsv` is the source; the `imports` lane enforces it on
non-test imports. The reasons:

- **Product never imports tooling, and tooling never imports product:** the
  gates cannot depend on the code they check, and `engine` ships nothing from
  `devtools/`.
- **Each package may import only what its rule lists.** Leaf packages import
  no first-party code; a command package composes them. A new package adds
  its rule in the same PR.

## Decisions new code must respect

- **One binary, `engine`, for macOS and Linux.**
- **The runtime is the standard library plus approved libraries:** `go.mod`'s
  direct requirements, each with no requirements and no cgo. A new one is a
  hard stop (`AGENTS.md`).
- **External tools use injectable CLI runners**, including `sqlite3`; the
  server exposes a versioned HTTP API.
- **Coordination derives decisions from authoritative records and host
  observations.** The server persists intent and execution identities.
  Coordinators never edit code; the server's coordinator merges its tasks.
  For server records and API, load `control-server.md`; for dispatch, merges,
  host reach and session signals, load `coordinator.md`.
- **The reconcile core reads only its View**, with no CLI assumptions.
- **Tooling is Go too.** A lane is one external command or a `dev check`
  subcommand, tested like product code.
