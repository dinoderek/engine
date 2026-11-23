# Dashboard and session view

> **Owns:** the dashboard's reads (`engine ui`) and the session view it and the CLI read. **Not here:** the records, API and inbox they read (`control-server.md`), the dashboard's writes (`dashboard-writes.md`), how a pass reads a session (`session-signals.md`).

For the records it reads, load `control-server.md`; to change its writes, `dashboard-writes.md`.

## Dashboard

`engine ui` serves a dashboard on `127.0.0.1:7332`; `--listen` takes another loopback address. It uses the CLI's server configuration and operator credential, never sent to the browser. Any local user reaching the port has operator rights; run it only on a single-user machine.

Reads forward only `list`, `plan-view`, `attempts`, `get`, `updates`, `session-view`, `inbox`, `overview` and `plan-detail`, for GET and HEAD to a loopback host name on any port, so an SSH tunnel works. It sends `Cache-Control: no-store`. When the control server fails, the page keeps its last data, marked stale.

A refresh reads three things together: `overview` (inbox, sessions and plan list, at `/api/overview`), `plan-detail` and the open thread's `updates` after its cursor. A server without them (a 400 or 404, learned once, retried after ten minutes) gets the earlier reads. Descriptions render as CommonMark plus tables: raw HTML stays text, images show alt text, and only absolute http, https and mailto links act. Messages are text.

The per-plan view lists unfinished prerequisites, including other plans' tasks. A todo task waiting to start has the plan's question as feedback. On Plans and Sessions, "Needs feedback" opens the question's thread at its update over it, on the plan's thread when it asked (`internal/ui/static/sessions.js`).

Each task has one summary: done is done; failed and needs-human are blocked; an active attempt, or a running or in-review state, is in progress; an unfinished prerequisite or a held todo task, or one under the plan's open question (its read's `question`), is blocked; todo under an approved plan is ready; anything else is blocked. Coordination does not change it.

## Session view

`session-view`, an operator-only read, returns one row per item's current, unended or resumable attempt, by item number. The fields in `internal/control/sessions.go` are the whole contract; credentials, screens, run files and bodies never join them.

- `last_seen` is when a host last reported the window live; it survives `unknown` and `exited` observations.
- `observed` is the harness's `working`, `waiting` or `limited`; `live` for an open window with no harness state; `exited` once a reachable host shows the window gone; otherwise `unknown`. An exited attempt stays listed until engine ends it.
- `feedback` is the item's unresolved question, including one its session asked on its plan. Waiting without one is not a feedback request.
- `latest` is the item's newest message update (`humanKinds` in `internal/control/sessions.go`) with a body, excluding coordinator notices. A task's preview never shows its plan's messages.

Observations live in an in-memory cache keyed by attempt, so a request never contacts a host. Coordinator passes store their host reads, and a background loop refreshes stale ones, one read in flight per host. The timings are in `internal/controlrun/observe.go`.
