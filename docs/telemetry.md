# Telemetry

> **Owns:** what is recorded about each run, in which format, and where it is stored. **Not here:** how sessions are started (`hosts-and-sessions.md`).

## Strategy

Store **raw** data in **standard formats** in **cheap object storage**.
Analysis comes later and can be re-run over the raw data. Nothing is
summarised away at capture time.

| Data | Format | Source |
| --- | --- | --- |
| Metrics, logs, events (tokens, cost, tool calls, API latency) | OpenTelemetry, as OTLP JSON lines | Both CLIs export OTel natively: Claude Code via `CLAUDE_CODE_ENABLE_TELEMETRY` and `OTEL_*` env vars, Codex via `[otel]` in its config |
| Transcripts | The harness's native JSONL, stored as-is (no standard exists) | `~/.claude/projects/…`, `~/.codex/sessions/…` |
| Run record | `run.json`, the engine's own index for the run | `engine start`, then the coordinator |
| Gate output | `.test-ai-results/` copied as-is | The worktree |

## Run record

One `run.json` per session, written at spawn and finalised at exit:

- **Identity:** run ID, repo, feature, task, host, harness, tier, model, and the
  harness session ID.
- **Times:** spawned, started, needs-human waits (each one, with duration),
  PR opened, merged, ended.
- **Outcome:** merged, failed, or abandoned. Plus the PR URL.
- **Process counts:** gate runs, review rounds, rulings by risk, deviations,
  conflict merges.
- **Pointers:** paths to the transcript, the OTel files and the gate output.

**On the host.** Each run has a directory,
`~/.local/share/engine/runs/<run-id>/`, holding `run.json`, `pane.log` (the
window's raw output) and `permissions.jsonl`: one JSON line per permission
request the hook denied and per action auto mode refused, with the time,
tool, input and decision (`Entry` in `internal/hook/permission.go`). The run ID
is `<repo>-<task>-<UTC yyyymmddThhmmssZ>`, the time its launch began. A preflight
check's directory, `check-<time>`, stays, with no `run.json`.

**At spawn,** `engine start` writes the identity it knows (all but the
harness session ID), the effort, mode, session name, tmux target (by window
id, such as `engine:@3`), branch, worktree, the pane log's path and the spawn
time, with `schema: 1`. `Record` in `internal/session/record.go` holds the
field names. Whatever writes the later fields keeps these and adds its own.

## Pipeline

1. Each machine that runs sessions runs an
   OpenTelemetry Collector with a file exporter. The harnesses point at it
   through their OTel config, and every session is tagged with its run ID as a
   resource attribute.
2. At session end, the coordinator gathers the run directory: `run.json`, the
   OTel slice for the run, the transcript and the gate output.
3. It syncs the run directory to object storage with `rclone`.

## Storage

Default: **Cloudflare R2**. Its free tier holds 10 GB, there are no egress
fees, and it speaks the S3 API. Backblaze B2 is the fallback. The bucket is
private.

```text
<bucket>/<repo>/<feature>/<task>/<run-id>/
  run.json
  otel/*.jsonl
  transcript/<harness>.jsonl
  gates/
```

Optional: the collector also forwards OTLP to a free hosted backend such as
Grafana Cloud for live dashboards. The bucket stays the system of record.

## Rules

- Transcripts can contain secrets. Scan each run directory with a secret
  scanner before upload, and keep the bucket private.
