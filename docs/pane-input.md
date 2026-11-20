# Pane input

> **Owns:** how the operator's answers typed into a session's pane reach the server: the typed-text log, the prompt hook, its run files and how a fake shows. **Not here:** the `pane-input` op and what it resolves (`control-server.md`), the adapter contract (`harnesses.md`), the host ops (`coordinator.md`).

## Why

A question holds its task in `needs-human`. An answer typed into the
session's pane reaches only the session, so the task would stay held and
its merge blocked. The prompt hook, `engine hook prompt --harness <name>`
on every submitted prompt, closes that gap with no command for the
operator. Claude Code and Codex both run it.

## Telling the operator's prompts from engine's

Before engine sends text to a run's session, it logs its SHA-256,
trimmed of surrounding white space, in the run's `typed.jsonl`: the spawn
logs the kickoff before the window opens, the coordinator each wake, and
what a resume submits (`continue`, or text left unsent), through the host's
`typed` op. Text it cannot log it does not type; a spawn that cannot log
its kickoff fails. A host whose engine predates the op has no prompt hook
either, so its refusal of the op lets the text through.

In a server run, the hook logs every submitted prompt in `prompts.jsonl`,
by hash; elsewhere it does nothing. Each logged hash accounts for one
prompt, so text the operator retypes later is still theirs.
A prompt from another session than the run's first, such as a reviewer the
session started, is ignored, and so is every prompt after Claude Code's
`/clear`, which starts a new session.

## Posting

The operator's prompt is queued in `pane-<n>.json` for a background
`engine hook prompt --harness <name> <run-dir> <n>`, since a harness may
write a prompt to its transcript only after its hooks run. It waits up to a
minute for the adapter's `Prompts` to hold the text exactly, then posts it
verbatim with the `pane-input` op, on the run's credential, saying whether
it was found and when it was submitted. The queue file keeps what it
vouches, so every retry is the same request under the same key.

An unanswering or failing server is retried for 15 minutes, waits growing
5 seconds to 2 minutes. An unposted prompt stays queued for the
session's next prompt to post again; a post locks its queue file, so two
never race. Posted, refused or unpostable, it is dequeued. `pane.jsonl` logs each
outcome: its update IDs or its error. If the background start fails, the
hook posts at once, without waiting or retrying.

## Detection, not prevention

A server run's spawn writes an Ed25519 key's seed to the owner-only
`pane-key` and returns its public half, which the coordinator stores on the
attempt. The hook signs the attempt ID, request key, time, transcript claim and
text with it. A post unsigned,
failing the attempt's key, or missing from the transcript is still
recorded and still resolves, but the server raises one coordinator note. The
note shows a post made without the hook; a session that reads its own run
directory can avoid it. `collect` never lists
`pane-key`.
