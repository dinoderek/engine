# Pane input

> **Owns:** how the operator's answers typed into a session's pane reach the server: the typed-text log, the prompt hook, its run files and how a fake shows. **Not here:** the `pane-input` op and what it resolves (`control-server.md`), the adapter contract (`harnesses.md`), the host ops (`coordinator.md`).

## Why

A question holds its task in `needs-human`. An operator who answers by
typing into the session's pane reaches the session but not the server,
which would keep the task held and its merge blocked. The prompt hook,
`engine hook prompt --harness <name>` on every submitted prompt, closes that
gap with no command for the operator. Claude Code and Codex both run it.

## Telling the operator's prompts from engine's

Before engine types into a run's pane, or launches it with a kickoff, it
logs the text's SHA-256, trimmed of surrounding white space, in the run's
`typed.jsonl`: the spawn logs the kickoff before the window opens, and the
coordinator logs each wake and `continue` through the host's `typed` op
before typing it. A text it cannot log it does not type, and a spawn that
cannot log its kickoff fails. A host whose engine predates the `typed` op
has no prompt hook either, so its refusal of the op lets the text through.

In a server run, the hook logs every submitted prompt in `prompts.jsonl`,
by hash; elsewhere it does nothing. Each logged hash accounts for one
prompt, so the operator typing the same text later is still the operator.
A prompt from another session than the run's first, such as a reviewer the
session started, is ignored.

## Posting

The operator's prompt is queued in `pane-<n>.json` and posted by a
background `engine hook prompt --harness <name> <run-dir> <n>`, since a
harness may write the prompt to its transcript only after its hooks run. The
post waits up to a minute for the adapter's `Prompts` to show the text in the
transcript, then sends it verbatim with the `pane-input` op, on the run's
credential, saying whether it was found. Each outcome, its update IDs or
its error, is a line in `pane.jsonl`; a failed post keeps its queue file.
If the background start fails, the hook posts at once without waiting.

## Detection, not prevention

A server run's spawn writes an Ed25519 key's seed to the owner-only
`pane-key` and returns its public half, which the coordinator stores on the
attempt. The hook signs the attempt ID and text with it. A post that is
unsigned, fails the attempt's key, or was missing from the transcript is
still recorded and still resolves, but the server raises one coordinator
note. A session set on faking a post can read its own run directory; the
note makes the attempt show, it does not stop it. `collect` never lists
`pane-key`.
