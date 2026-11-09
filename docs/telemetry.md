# Telemetry

> **Owns:** what is recorded about each run, and where on the host it is kept. **Not here:** how sessions are started (`hosts-and-sessions.md`).

## Run record

Each run has a directory on its host,
`~/.local/share/engine/runs/<run-id>/`. The host reads it, and the
coordinator reads it through the `collect` and `status` host ops; nothing is
uploaded.

`run.json` is the run's index, written once at spawn. The host's `spawn` op
writes the identity its caller passed (run ID, repo, task, host, harness,
tier, model, effort, mode, and whether it is heavy: `hosts-and-sessions.md`,
"Hosts"), the session name, tmux target (by window id, such as `engine:@3`),
branch, worktree, the pane log's path and the spawn time, with `schema: 1`.
`Record` in `internal/session/record.go` holds the field names.

The directory also holds `pane.log` (the window's raw output) and
`permissions.jsonl`: one JSON line per permission request the hook denied or,
in Codex, left to auto-review, and per action auto mode refused, with the
time, tool, input and decision (`Entry` in `internal/hook/permission.go`).
Run IDs are `<repo>-<task>-<UTC yyyymmddThhmmssZ>`, using launch time. A preflight
check's directory, `check-<time>`, stays, with no `run.json`. A hook that finds
its run without `ENGINE_RUN_DIR` scans these
directories for the `run.json` whose worktree holds the event's `cwd`.

`harness-state.json` is the state hook's latest state, with the harness's
session and turn IDs. Each adapter may keep its own files there too, named
in its package under `internal/harness/`: Claude's `quota.json` (subscription
usage), `limit.json` (the current usage stop) and `limits.jsonl` (every
stop's full input); Codex's `codex-label.json` (the attempts to label a
joined thread).

The coordinator logs each resume attempt with the task, timestamp and
`resumed-for=<resets_at>`. Its notes retain the attempt and limit-alert times
per reset and session; for recovery semantics, load `coordinator.md`,
“Limit recovery”.
