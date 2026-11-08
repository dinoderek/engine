# Supervise engine run

`engine run` coordinates the feature; you watch it and fill only the gaps it
cannot. Kickoff: `Supervise <feature>`. Every gap you fill is a defect in
`engine run`, so you record each one.

## Start

- **Server plan** (`p<n>`, approved): `engine run <plan>` records the intent
  and returns; the server coordinates. Check every 10 minutes: `engine ps`,
  `engine inbox --operator`, and `engine updates <id>` for the plan and each
  task (`engine ui` is the operator's view). Below, a feature comment is
  `engine tell <plan> <text>`; done is the plan `done`.
- **GitHub feature** (`owner/repo#<n>`): run `engine run <feature>` in its
  own tmux window, `run-<repo>-<n>` in the session `engine`, its output
  appended to a log you name in your first feature comment. Check every 10
  minutes: its log, `engine ps --github --json`, and the feature's comments.

## You may

- **Relay an answer** the operator gave you, with `engine tell <task> <text>`
  (`t<n>`, or `<repo>-<task>` in GitHub mode). One for several of a plan's
  tasks goes once to the plan, which wakes each waiting task. Answer a server
  inbox question with `engine reply <seq> <text>`; leave `engine resolve`
  and the state change, the operator's decision, to the operator unless they
  gave it to you.
- **Answer a stall** `engine run` raised, with `engine tell`, when the answer
  is not a hard stop: for example "your claim landed, carry on". This
  reaches an unclaimed session too.
- **Restart `engine run`** (GitHub mode) when it has died, or after an
  engine fix merges. Never restart the control server.
  Restart once its log shows a pass has ended. To pick up a fix, rebuild
  first: `GOBIN=~/.local/bin go install ./cmd/engine` from an
  up-to-date engine checkout. `engine run` is stateless, so it resumes.

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
