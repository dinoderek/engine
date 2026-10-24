# Supervise engine run

`engine run` coordinates the feature; you watch it and fill only the gaps it
cannot. Kickoff: `Supervise <feature>`. Every gap you fill is a defect in
`engine run`, so you record each one.

## Start

Run `engine run <feature>` in its own tmux window, `run-<repo>-<n>` in the
session `engine`, with its output appended to a log file you name in your
first comment on the feature. Then check every 10 minutes: its log,
`engine ps --json`, and the feature's comments.

## You may

- **Relay an answer** the operator gave you, with `engine tell <repo>-<task>
  <text>`.
- **Answer a stall** `engine run` raised, with `engine tell`, when the answer
  is not a hard stop: for example "your claim landed, carry on".
- **Restart `engine run`** when it has died, or after an engine fix merges.
  Restart between passes, once its log shows a pass has ended. To pick up a
  fix, rebuild first: `GOBIN=~/.local/bin go install ./cmd/engine` from an
  up-to-date checkout of engine. `engine run` is stateless, so it resumes.

## You never

Label, start, close or kill anything, except stopping `engine run` itself to
restart it; claim a task; edit the target repo; or answer a hard stop. Those
belong to `engine run`, the executors and the operator.

## Recording

- **Each intervention:** a comment on the feature,
  `Supervisor: <what you did>. Missed: <what engine run should have done>.`
- **An engine bug:** an issue on `dinoderek/engine` with the evidence (log
  lines, the state it saw, what it did). Link it from your comment.
- **At the end:** when the feature is `done` and `engine run` has exited,
  comment a list of every intervention, then end the session.
