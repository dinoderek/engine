# Dashboard writes

> **Owns:** what `engine ui` accepts from a browser: `--host`, the Origin check, the write routes, request keys and the page's write helper; the Inbox tab and plan and task control built on them. **Not here:** the reads, the stale marking and the session view (`dashboard.md`), the operations the writes become (`control-server.md`).

## Reaching it

`--listen` stays loopback. `--host <name>` also admits requests whose `Host` is that DNS name, on any port, so a proxy on the tailnet can pass the dashboard's traffic on; loopback names keep working. There is no identity check: reaching the port is operator access, for a local user and for whoever reaches it over the tailnet. The operator credential never reaches the browser, and the CSP keeps `default-src 'self'`.

The process is stateless, so any number may run against one server. The proxy must pass the `Host` header through unchanged: the Origin check compares against it.

## Which writes

A write is a `POST` of one JSON object, at most 64 KiB, with only the fields its route lists. Every body carries `key`. Anything else is refused before the control server is called: another method, a non-JSON body, an unknown or missing field, an oversized body, a malformed id or update number, an id of the wrong kind for the route.

| Route | Fields besides `key` | Control operation |
| --- | --- | --- |
| `/api/updates/{n}/resolve` | `choice?`, `note?` | `resolve` |
| `/api/updates/{n}/reply` | `body` | `reply` |
| `/api/updates/{n}/ack` | none | `ack` |
| `/api/updates/{n}/ack-all` | none | `ack-all` before `n + 1` |
| `/api/items/{id}/say` | `body` | `say`, as `engine tell` |
| `/api/plans/{id}/approve` | `version` | `approve` |
| `/api/plans/{id}/run`, `/stop` | none | `run`, `stop` |
| `/api/tasks/{id}/stop`, `/resume` | none | `task-stop`, `task-resume` |
| `/api/tasks/{id}/retry` | `version` | `state` to `todo` |
| `/api/tasks/{id}/complete-external` | `version`, `pr` | `complete-external` |

Writes have their own allowlist (`writeOps` in `internal/ui/writes.go`), apart from the reads'. Resolve needs a `choice` or a `note`. `choice` is an index into the question's choices: the process reads the updates from the question on, finds it, and sends the choice's text, a newline, then the note. The read lists a resolved question too, so a resend of an applied resolve replays; one resolved elsewhere gets the server's `409`. `version` is the one the page read; a stale one is the server's 409.

## Origin and content type

The `Origin` must be `http` or `https` with no path and the same host and port as the request's `Host`. A missing, opaque (`null`) or foreign `Origin` is `403`; a body that is not `application/json` is `415`. This, with the host check, stops a page on another site from writing through the operator's browser.

## Request keys

The server applies a key once: a resend with the same key and body returns the first result, and the same key with another body is `409`. The page's helper (`internal/ui/static/writes.js`) makes one key when a form opens, with `crypto.randomUUID()`, or from `crypto.getRandomValues()` where the page is not a secure context, as over plain http by its `--host` name, and resends it after no answer or a 5xx. A 2xx or 4xx ends the attempt and the next send makes a new key. The control server's status and reason pass through for `400`, `404` and `409`; a server or transport failure is `502`, with the server's message for a server error and no reason.

## Inbox

`GET /api/inbox` (`internal/ui/inbox.go`) adds no control operation. It reads `inbox` after a cursor, 200 updates a page until a short one, then `list` for titles, repositories and pull requests and `session-view` for each session's latest line. It goes through the snapshot cache like the read views: a failed read returns the last inbox marked stale. Cards list open questions, oldest first, then unacknowledged messages, oldest first, across repositories. A plan question a task's session asked takes that session's pull request and latest line. Only an absolute http or https pull request URL becomes a link; bodies are text.

The Inbox tab is the first and the default page (`internal/ui/static/inbox.js`). A choice button resolves with that choice and the note; Answer sends the note alone; Reply and Say go to their routes; Acknowledge is offered on messages, because an acknowledged question stays open until resolved. Acknowledge all messages, shown while a message card is listed and the view is fresh, acknowledges every informational message up to the newest card's update, never a question; one that arrives meanwhile stays. It resends its key after no answer or a 5xx, like a card. A refresh keeps the cards on screen, so typing and focus stay.

- A 409 keeps the typed text, shows the server's message and reloads. A card the server no longer lists stays, marked, until dismissed.
- A note is a draft in `localStorage`, `engine.draft.<update>`, from the first keystroke until it is sent. A fresh read drops the drafts of cards it no longer lists.
- The app badge, where the browser has one, counts open questions, not messages.
- `/manifest.webmanifest` and its icons let a phone install the page; it starts on the Inbox.

Below 800px every view is one column with no page-level horizontal scroll, and every control is at least 44px by 44px. A link inside a description or message is 44px tall and as wide as its text.

## Control

The plan view and the item panel offer the writes an item's state allows. The process decides which (`internal/ui/control.go`) and sends them as `actions`, with the item's `version`, on the plan view (the plan and each task row) and on an item read; the page offers exactly those and never works out a state rule itself. The server still decides every write, and a refusal shows its message.

| Action | Offered when |
| --- | --- |
| `approve` | the plan is in `prepare` |
| `run` | the plan is approved, not done and not coordinating |
| `stop` | the plan is coordinating |
| `task-stop` | the task is `running`, `in-review` or `needs-human` with an unended attempt |
| `resume` | the task is held |
| `retry` | the task is `failed` |
| `complete-external` | the task is `todo` or `failed` with no unended attempt |

An item read finds an unfinished task's attempt through `attempts`, because its state does not say whether the attempt has ended.

Approve, plan stop, task stop, retry and complete-external open a confirmation sheet first (`internal/ui/static/control.js`): it names the item, its state and the effect, and sends nothing until confirmed. Complete-external also asks for the pull request URL. Run and resume go straight through. The sheet sends, for approve, retry and complete-external, the version the page read when it opened, so an edit made meanwhile is the server's 409. Cancel and Escape wait while a confirmation is in flight.

- A `409` closes the sheet, refreshes the plan view and item, and says what changed: the item's new state, or that someone else changed it. A `400` keeps the sheet and the typed URL. No answer, or a server error, keeps the sheet and resends the same request key with the text locked; the page keeps a key only for the version of the item it was made on, so a changed item never replays an old result.
- A result stays beside the item's buttons across refreshes.
- On a phone the sheet rises from the bottom edge, within the viewport, with 44px targets; the plan table shows each task's actions on its own line.
