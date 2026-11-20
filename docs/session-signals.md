# Session signals

> **Owns:** how a coordinator pass reads whether a session is alive and takes input, stalls, early deaths and limit recovery. **Not here:** dispatch, recovery of other ended attempts and the merge queue (`coordinator.md`), what each harness observes (`harnesses.md`), what a run records (`telemetry.md`).

Agent state is never scraped from the pane: task state and questions are server records. Two sources say whether a session is alive and takes input:

1. **The harness** gives each window of session `engine` a `state` (`working`, `waiting` or `limited`) from the observation of the harness its newest `run.json` names (`harnesses.md`). A window with no run included is unknown.
2. **tmux.** A window whose command has not exited is alive; a dead window is dead whatever the harness says.

A session is **idle** when `waiting`, or `working` with `background` set. Only idle sessions get wake-up pointers.

**Stalls.** Limited, queued and background-only sessions never stall. A dispatched session that has not claimed its task 5 minutes after it started is noticed once, and one that ended before claiming is an early death. A session waiting 10 minutes on an open task with no hard stop or question has stopped outside the protocol; the operator is notified once, naming the sign-in for a signed-out harness. A denial at most 10 minutes older than the wait makes the stall a Permission hard stop naming the tool, input and reason.

**Early death.** A window gone before its session claimed the task, which neither wrote to the server nor was seen live for `ClaimWait`, is not a vanish unless the vanish rule (`coordinator.md`, "Dispatch and recovery") already applies. The first such death per task, across attempts, ends the attempt `early-death` with its pane log tail recorded and, with a notice, returns a running task to `todo` for the next pass to redispatch. The second ends the attempt and task `failed`, and its notice carries both attempts' tails. Deaths of sessions that claimed never count; an early death after one is still the first. An operator's retry of a failed task counts the earlier early deaths, so one more fails it at once.

**Limit recovery.** Never resume `needs-human`; recheck the task's state before typing. Once the reset plus 2 minutes has passed, each check types literal `continue` then Enter into a window still `limited`, or `waiting` with a positive limit `resets_at`; other sessions and unknown resets are left alone. The attempt is noted first, so a failed Enter never duplicates text. Waiting with a limit reset also clears the stall timer. Still `limited` 10 minutes after the attempt, or a reset over 24 hours away (no typing), gives one notice per reset and session. Recovery covers planners, executors and cleanup turns.
