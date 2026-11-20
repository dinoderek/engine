# Dashboard and session view

> **Owns:** the read-only dashboard (`engine ui`) and the session view it and the CLI read. **Not here:** the records, API and inbox they read (`control-server.md`), how a pass reads a session (`session-signals.md`).

`engine ui` and `session-view` read the server's records (`control-server.md`) and never write them.

## Dashboard

`engine ui` serves a read-only dashboard on `127.0.0.1:7332`; `--listen` chooses another loopback address. It uses the CLI's server configuration and operator credential, which never reaches the browser. Any local user reaching the port reads with operator rights; run it only on a single-user machine.

The process forwards only read operations (`list`, `plan-view`, `attempts`, `get`, `updates`, `session-view`) and answers GET and HEAD for a loopback host name on any port, so an SSH tunnel works. It sends `Cache-Control: no-store`. When the control server fails, the page keeps its last data, marked stale. Descriptions render as CommonMark plus tables: raw HTML stays text, images show alt text, and only absolute http, https and mailto links act. Messages are text; the CSP allows only the page's scripts.

The per-plan view lists unfinished prerequisites, including other plans' tasks. A todo task waiting to start has the plan's question as feedback. On both tabs, "Needs feedback" opens the question's thread at its update over the tab, on the plan's thread when it asked (`internal/ui/static/sessions.js`).

Each task has one summary: done is done; failed and needs-human are blocked; an active attempt, or a running or in-review state, is in progress; an unfinished prerequisite or a held todo task, or one under the plan's open question (its read's `question`), is blocked; todo under an approved plan is ready; anything else is blocked. Coordination does not change it.

## Session view

`session-view`, an operator-only read, returns one row per item's current, unended or resumable attempt, by item number. The fields in `internal/control/sessions.go` are the whole contract; credentials, screens, run files and bodies never join them.

- `role` is `planner` for a plan and `executor` for a task.
- `last_seen` is when a host last reported the window live; it survives `unknown` and `exited` observations.
- `observed` is the harness's `working`, `waiting` or `limited`; `live` for an open window with no harness state; `exited` once a reachable host shows the window gone; otherwise `unknown`. An exited attempt stays listed until engine ends it.
- `feedback` is the item's unresolved question, including one its session asked on its plan. Waiting without one is not a feedback request.
- `latest` is the item's newest message update (`humanKinds` in `internal/control/sessions.go`) with a body, excluding coordinator notices. A task's preview never shows its plan's messages.

Observations live in an in-memory cache keyed by attempt, so a request never contacts a host. Coordinator passes store their host reads, and a background loop refreshes stale ones, one read in flight per host. The cache drops ended and replaced attempts and starts empty with the server. An unreachable host, unreadable run records or an old observation give `unknown`; the timings are in `internal/controlrun/observe.go`.
