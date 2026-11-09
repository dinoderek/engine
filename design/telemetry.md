# Telemetry upload (ephemeral design)

Designed, not built. What exists today (the run directory and `run.json` as
written at spawn) is in `docs/telemetry.md`; this draft is the rest. When a
part ships, move it there and delete it here.

## Strategy

Store raw data in standard formats in object storage for repeatable analysis.

| Data | Format | Source |
| --- | --- | --- |
| Metrics, logs, events (tokens, cost, tool calls, API latency) | OpenTelemetry, as OTLP JSON lines | Both CLIs export OTel natively: Claude Code via `CLAUDE_CODE_ENABLE_TELEMETRY` and `OTEL_*` env vars, Codex via `[otel]` in its config |
| Transcripts | The harness's native JSONL, stored as-is (no standard exists) | `~/.claude/projects/…`, `~/.codex/sessions/…` |
| Run record | `run.json`, the engine's own index for the run | The host's `spawn`, then the coordinator |
| Gate output | `.test-ai-results/` copied as-is | The worktree |

## Run record, finalised at exit

`run.json` is written at spawn (built). The coordinator finalises it at exit,
keeping the spawn fields and adding:

- **Identity:** the harness session ID.
- **Times:** started, needs-human waits (each one, with duration), PR opened,
  merged, ended.
- **Outcome:** merged, failed, or abandoned. Plus the PR URL.
- **Process counts:** gate runs, review rounds, rulings by risk, deviations,
  conflict merges.
- **Pointers:** paths to the transcript, the OTel files and the gate output.

## Pipeline

1. Each machine that runs sessions runs an OpenTelemetry Collector with a
   file exporter. The harnesses point at it through their OTel config, and
   every session is tagged with its run ID as a resource attribute.
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
