# Telemetry

> **Owns:** what is recorded about each run, and where on the host it is kept. **Not here:** how sessions are started (`hosts-and-sessions.md`).

## Run record

Each run has a directory on its host, `~/.local/share/engine/runs/<run-id>/`.
The host reads it, and the coordinator reads it through the `collect` and
`status` host ops; nothing is uploaded.

`run.json` is the run's index, written once at spawn by the host's `spawn` op
with the caller's identity, the session name, tmux target, branch, worktree,
pane log path and spawn time; `Record` in `internal/session/record.go` holds
the field names. Run IDs are `<repo>-<task>-<UTC yyyymmddThhmmssZ>`, using
launch time. A preflight check's directory, `check-<time>`, stays, with no
`run.json`.
A hook that finds no `ENGINE_RUN_DIR` scans these directories for the
`run.json` whose worktree holds the event's `cwd`.

The directory also holds `pane.log` (the window's raw output) and
`permissions.jsonl`: one line per permission request the hook denied or, in
Codex, left to auto-review, and per action auto mode refused (`Entry` in
`internal/hook/permission.go`).

`harness-state.json` is the state hook's latest state, and `quota.json` the
newest quota reading a harness's hooks record. Each adapter may keep its own
files there too, named in its package under `internal/harness/`.

The coordinator logs each resume attempt with the task, timestamp and
`resumed-for=<resets_at>`, and its notes retain the attempt and limit-alert
times per reset and session. For recovery semantics, load `coordinator.md`,
"Limit recovery".
