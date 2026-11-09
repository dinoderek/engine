# Telemetry

> **Owns:** what is recorded about each run, and where on the host it is kept. **Not here:** how sessions are started (`hosts-and-sessions.md`).

## Run record

Each run has a directory on its host,
`~/.local/share/engine/runs/<run-id>/`. The host and the coordinator read it;
nothing leaves the host.

`run.json` is the run's index, written once at spawn. The host's `spawn` op
writes the identity its caller passed (run ID, repo, task, host, harness,
tier, model, effort, mode, and whether it is heavy: `hosts-and-sessions.md`,
"Hosts"), the session name, tmux target (by window id, such as `engine:@3`),
branch, worktree, the pane log's path and the spawn time, with `schema: 1`.
`Record` in `internal/session/record.go` holds the field names. A writer that
adds a field keeps the others.

The directory also holds `pane.log` (the window's raw output) and
`permissions.jsonl`: one JSON line per permission request the hook denied or,
in Codex, left to auto-review, and per action auto mode refused, with the
time, tool, input and decision (`Entry` in `internal/hook/permission.go`).
Run IDs are `<repo>-<task>-<UTC yyyymmddThhmmssZ>`, using launch time. A preflight
check's directory, `check-<time>`, stays, with no `run.json`. A hook that finds
its run without `ENGINE_RUN_DIR` (`codex.md`, "Session signals") scans these
directories for the `run.json` whose worktree holds the event's `cwd`.

`quota.json` holds Claude subscription usage: `harness`, `observed_at` (epoch
seconds), and `windows` with optional `five_hour` and `seven_day` entries, each
with `used` (percent) and `resets_at` (epoch seconds). The status-line hook
atomically replaces it only when usage or reset times change.

`limit.json` holds Claude's current usage stop: `since`, `resets_at` (epoch
seconds; zero means unknown), `error`, and optional `notification_type` for
stale or disabled auto-resume. Hooks replace it atomically and remove it
when a prompt or automatic resume fires. `limits.jsonl` retains each stop
with those fields and its complete hook `input`, including error details,
for inspection after the marker clears.

`harness-state.json` retains Codex session and optional turn IDs with each
hook state, so a newer prompt cannot inherit an older failed turn. Stored
failed-turn input is read through app-server; it is not a limit hook event.

`codex-label.json` records the attempts to label a joined Codex thread: the
thread ID, title, project, repo, project root, the latest attempt's time and
error, and the attempt count; `codex-label.lock` is held during one. For how
the label is set, load `codex.md`, "Apps".

The coordinator logs each resume attempt with the task, timestamp and
`resumed-for=<resets_at>`. Its notes retain the attempt and limit-alert times
per reset and session; for recovery semantics, load `coordinator.md`,
“Limit recovery”.
