# Telemetry

> **Owns:** what is recorded about each run, in which format, and where it is stored. **Not here:** how sessions are started (`hosts-and-sessions.md`).

## Strategy

Store raw data in standard formats in object storage for repeatable analysis.

| Data | Format | Source |
| --- | --- | --- |
| Metrics, logs, events (tokens, cost, tool calls, API latency) | OpenTelemetry, as OTLP JSON lines | Both CLIs export OTel natively: Claude Code via `CLAUDE_CODE_ENABLE_TELEMETRY` and `OTEL_*` env vars, Codex via `[otel]` in its config |
| Transcripts | The harness's native JSONL, stored as-is (no standard exists) | `~/.claude/projects/…`, `~/.codex/sessions/…` |
| Run record | `run.json`, the engine's own index for the run | `engine start`, then the coordinator |
| Gate output | `.test-ai-results/` copied as-is | The worktree |

## Run record

`run.json` is written at spawn and finalised at exit:

- **Identity:** run ID, repo, feature, task, host, harness, tier, model,
  whether it is heavy (`hosts-and-sessions.md`, "Hosts"), and the harness
  session ID.
- **Times:** spawned, started, needs-human waits (each one, with duration),
  PR opened, merged, ended.
- **Outcome:** merged, failed, or abandoned. Plus the PR URL.
- **Process counts:** gate runs, review rounds, rulings by risk, deviations,
  conflict merges.
- **Pointers:** paths to the transcript, the OTel files and the gate output.

**On the host.** Each run has a directory,
`~/.local/share/engine/runs/<run-id>/`, holding `run.json`, `pane.log` (the
window's raw output) and `permissions.jsonl`: one JSON line per permission
request the hook denied or, in Codex, left to auto-review, and per action
auto mode refused, with the time,
tool, input and decision (`Entry` in `internal/hook/permission.go`). Run IDs are `<repo>-<task>-<UTC yyyymmddThhmmssZ>`, using launch time. A preflight
check's directory, `check-<time>`, stays, with no `run.json`. A hook that finds
its run without `ENGINE_RUN_DIR` (`codex.md`, "Session signals") scans these
directories for the `run.json` whose worktree holds the event's `cwd`.

**At spawn,** the host's `spawn` op writes the identity its caller passed (all but the
harness session ID), the effort, mode, session name, tmux target (by window
id, such as `engine:@3`), branch, worktree, the pane log's path and the spawn
time, with `schema: 1`. `Record` in `internal/session/record.go` holds the
field names. Whatever writes the later fields keeps these and adds its own.

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

## Pipeline

1. Each machine that runs sessions runs an
   OpenTelemetry Collector with a file exporter. The harnesses point at it
   through their OTel config, and every session is tagged with its run ID as a
   resource attribute.
2. At session end, the coordinator gathers the run directory: `run.json`, the
   OTel slice for the run, the transcript and the gate output.
3. It syncs the run directory to object storage with `rclone`.

## Storage

**Cloudflare R2**: 10 GB free, no egress fees, S3 API. Backblaze B2 is the
fallback. Keep the bucket private.

```text
<bucket>/<repo>/<feature>/<task>/<run-id>/
  run.json
  otel/*.jsonl
  transcript/<harness>.jsonl
  gates/
```

The bucket is the system of record.

## Rules

- Transcripts can contain secrets. Scan each run directory with a secret
  scanner before upload, and keep the bucket private.
