# Control server build

The operator approved the server sketch in this session and directed a direct
build because the GitHub-backed engine workflow is unavailable. Planning,
review and evidence stay local; no issue or PR is required for this build.

## Outcome

One engine serve process owns plans, tasks, attempts, durable updates, operator
messages and repository merge locks. The CLI uses an authenticated API. The
server coordinates sessions through the existing host operations without a
machine-user GitHub token. GitHub remains a code, PR and CI integration.

## Implementation

- SQLite through its CLI, with transactional state/history and request dedupe.
- Private HTTP endpoint; operator and worker credentials, derived authorship.
- Versioned plan/task changes, explicit approval and linked operator replies.
- Atomic task claims and non-expiring repository locks owned by attempts.
- Existing coordinator decisions and placement reused through a server adapter.
- Restart reconciliation and durable dispatch identities prevent duplicate work.
- Host operations support the operator's ordinary git credentials for server runs.
- Local gates and a fresh independent review; leave the branch for inspection.

## Validation

Real SQLite and HTTP tests cover persistence, concurrency, rollback, retries,
permissions, inbox semantics and lock ownership. Coordinator and host tests cover
dispatch and restart/outage behavior without contacting GitHub. Run ./dev test.

## Evidence

- Every mandatory lane passed through ./dev test; statement coverage 91.0%.
- Local live smoke passed through CLI, HTTP and SQLite for repository, plan,
  task, task listing, update and operator inbox operations.
- Restarting the real server process retained the smoke records.
- Fresh reviewer found and verified fixes for dispatch observation/intent races,
  partial run records, hard-stop failure recovery, worker read scope, inherited
  machine credentials, structured card fields and per-reset quota recovery.
- Harness: Codex. Exact runtime model identity is not exposed by this session.
- No deployment, GitHub issue, PR, push or CI run was performed.

## Left to implementation

Internal package layout, IDs, API routes and command flags. No deployment,
automatic GitHub publishing, phone push provider or bulk telemetry upload.
