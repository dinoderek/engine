# Dashboard writes

> **Owns:** what `engine ui` accepts from a browser: `--host`, the Origin check, the write routes, request keys and the page's write helper. **Not here:** the reads, the stale marking and the session view (`dashboard.md`), the operations the writes become (`control-server.md`).

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

The server applies a key once: a resend with the same key and body returns the first result, and the same key with another body is `409`. The page's helper (`internal/ui/static/writes.js`) makes one key with `crypto.randomUUID()` when a form opens and resends it after no answer or a 5xx. A 2xx or 4xx ends the attempt and the next send makes a new key. The control server's status and reason pass through for `400`, `404` and `409`; a server or transport failure is `502`, with the server's message for a server error and no reason.
