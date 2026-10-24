# Learnings from the BOGA3#881 run (ephemeral)

What went wrong or slow, the evidence, and a proposed fix for each. Items
marked **Q** need an operator decision before they become work. Numbers and
raw data: `README.md`.

## Fixed during the run

| # | Problem | Evidence | Fix |
| --- | --- | --- | --- |
| F1 | Executors stalled on Claude's "read outside the working directories" prompt when they read the skill's references with the Read tool. With `cat` they did not, so it happened about half the time. | #883 (78 min lost), #885 | engine#28: `engine start` passes `--add-dir <plugin dir>` |
| F2 | The same prompt fired when an executor read a screenshot from `/tmp`. | #888 | engine#29: read-only `Read(//tmp/**)` rules. Not `--add-dir /tmp`, which would load skills anyone can plant there |
| F3 | Finished executors never exited, so their windows held slots until the coordinator killed them. | All 0.3.1 sessions | engine#28: a hands-off executor closes its own window. Confirmed on #886 and #888 |
| F4 | Closed tasks kept `engine:in-review`. | All 0.3.1 tasks | engine#28: the executor removes it after the merge |

## Coordinator (`engine run`)

**C1. There is no `engine run`.** A Claude session ran the loop by hand: about 15
passes over 6 hours. Each pass did the same few things, which is effectively
the reconcile spec:
- read `engine ps --json` and the task labels and blockers;
- `engine start` ready tasks while under the slot cap;
- move the feature's label;
- close finished windows;
- relay operator answers.

It worked, but it needed a live Claude session and a Mac on power.
- Proposal: build `engine run` next. Use this run's passes as its acceptance
  scenarios, with a fake harness.

**C2. Nobody closes the feature issue.** #881 ended `engine:done` and open.
- Proposal: the coordinator closes it, as completed, when it sets `done`,
  after posting a summary comment: task PRs, the acceptance result, tasks
  still carrying `ruling-review`. One line in `execution-protocol.md` and in
  `coordinator.md`.

**C3. A stalled start before the claim is invisible.** #883 sat on a
permission prompt for 78 minutes. `engine ps` showed nothing, because there
was no claim yet. The 10-minute stall rule keys off claimed sessions.
- Proposal: the coordinator also watches the windows it opened. A window
  whose task has no claim after about 5 minutes, or that `claude agents`
  reports `waiting`, is a stall: @mention the operator with the last screen
  lines.

**C4. No order among ready tasks.** At 15:05 four tasks were ready for three
slots. I picked #887, #885 and #884 by hand, and #886 waited 72 minutes.
- Proposal: order ready tasks by their downstream depth (how many tasks wait
  on them), then deep tier first, then card order.
- **Q:** is critical-path-first the rule you want, or plain card order?

**C5. Reaping.** Even with F3, a session can crash or end after its merge
without closing. Then its window holds a slot.
- Proposal: each pass, close any engine window whose task is closed and whose
  worktree is gone, and log it.

## Executors and the protocol

**E1. Sibling tasks decided one shared open question two ways.** #884 lists a
headerless family under the previous family's header. #885 lists headerless
families first. Acceptance (#888) found it and recorded it as a deviation.
Both tasks inherited the question from the feature's `muscle.family-sections`
fact.
- Proposal, either or both:
  - **Plan:** lint flags an Open question that appears on more than one card,
    and the planner gives it to one card. The others list it under Decided:
    "per #<owner>".
  - **Execute:** a ruling that touches a fact other open cards cite gets a
    heads-up comment on those cards, as API changes already do.
- **Q:** which one, or both?

**E2. Cross-task notes work, but can go stale.** Every note went on the
target task's issue, which is right. Two problems:
- Some were posted while the sender's PR was still open ("PR pending"), so
  the PR could still change after the note went out.
- One note went to three issues at once (#884, #885, #886).

Proposal: post notes after the merge, saying what merged. Address each one to
the task that needs it, rather than to everyone who might.

**E3. Rulings need plain words.** The operator could not answer #884's ruling
until "Track switch" was explained. Every ruling was `med`, and six of seven
tasks raised `ruling-review`, so the label carries little signal.
- Proposal: `rulings.md` asks that each `med` or `high` ruling state, in
  product terms, what a user would notice and what the alternative was.
- **Q:** should `med` rulings stop raising `engine:ruling-review` and stay
  only in the PR and the issue, keeping the label for `high`?

**E4. Answers reached executors by three routes.** These were the operator's
issue comments, the coordinator typing into a session with `send-keys`, and
Remote Control. Comments are read only at steps 3, 8 and 10, so an answer
posted mid-step waits. Typing into the session reached the executor at once.
- Proposal: keep comments as the record. `engine` gets a `tell <task>
  <text>` command, which posts the comment and also types a pointer to it into
  the live session.
- **Q:** worth building, or is Remote Control enough?

**E5. Moving `main` cost re-gating.** #884 and #885 both had to merge `main`
after a sibling landed, and rerun their gates. That was most of #885's 146
minutes, because of the iOS lanes.
- Proposal: measure it from the transcripts first. If it dominates, the
  coordinator staggers the merge phase: one in-review task merges at a time,
  and the others wait at "ready to merge" before their final gate.
- **Q:** acceptable as is for now?

**E6. A doc budget filled up mid-feature.** `docs/product/` hit its
5000-word cap during #889. #883 then needed an operator ruling (trim, don't
raise) to add its facts.
- Proposal: during Prepare, the planner checks the headroom in every doc
  budget the feature's docs-to-update touch. A feature that would overflow
  one settles it before execution: a trim task first, or an agreed raise.

**E7. Card size.** #891 added 3,107 lines against the ~2,000 aim. Much of it
may be tests and migrations.
- **Q:** should the size aim count only non-test lines, or should the planner
  have split storage from sync?

## Setup and operations

**S1. `go install` put `engine` in `~/go/bin`, which was not on `PATH`.**
Without the binary, the merge guard does nothing.
- Proposal: `operator-setup.md` uses `GOBIN=~/.local/bin go install
  ./cmd/engine` on every host, as it already does on the devbox.
- Proposal: `engine start` refuses when `engine` is not on the `PATH` the
  window will get.

**S2. `claude plugin update` picked the wrong scope.** In a checkout that
enables the plugin in `.claude/settings.json`, it updated the project-scoped
copy and left the user-scoped one, which the target repo's sessions use.
- Proposal: `operator-setup.md` says `claude plugin update engine@engine
  --scope user`.

**S3. One-time Claude prompts are found by stalling.** Each of F1 and F2
cost a stall.
- Proposal: a `engine doctor` (or `engine start --check`) that launches a
  throwaway session in tmux and drives the known risky actions: read the
  skill, read `/tmp`, run `gh`, close its window. It reports any prompt it
  sees.

**S4. Hosts drift.** The Mac got `max_sessions = 3` and two launch-line
edits. The devbox has none of them, nor the new binary or plugin.
- Proposal: `engine ps` (or the coordinator at start) compares each host's
  `hosts.toml` hash and `engine version`, and warns on a mismatch.

**S5. Telemetry was assembled by hand.** `README.md`'s tables came from
transcripts and `gh`.
- Proposal: this is the minimum the telemetry design should record per run:
  - start, claim, PR-open and merge times;
  - turns, tool calls, reviewer runs;
  - tokens by kind;
  - rulings by risk;
  - stalls, with their cause.

**S6. Claude's suggested next prompt looks like operator input.** The idle
input box shows a suggested prompt. I read one as the operator's unsent
draft. Nobody typed it.
- **Q:** disable prompt suggestions in engine sessions, if the CLI allows it,
  or just know about it?

## Suggested next steps

In order:
1. **Small protocol and doc edits:** C2, E2, E3 (wording), E6, S1, S2.
2. **`engine run`:** C1, with C3, C4 and C5 built in.
3. **Telemetry collection,** in the S5 shape.
4. **A preflight check:** S3, S4.
5. **After the Q answers:** E1, E4, E5, E7.
