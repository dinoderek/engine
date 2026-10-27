# Run: quota, limit recovery and harness ratio (ephemeral)

Acceptance evidence for engine#131, executed by engine#138 on 2026-10-27.
The acceptance executor runs on `mac`; a separate `engine run` drives the
fixture engine#148 across `mac` and `devbox`.

## Acceptance status

**FAIL for real Codex limit detection and automatic recovery (O2/O3).**
Quota display and the first three ratio choices are proved live; the complete
fixture is still finishing after a manual recovery. Passing scenario tests
do not override the real failure below. Defect
[engine#155](https://github.com/dinoderek/engine/issues/155) tracks it.

## Setup

The tested engine commit is `b6262d0a2cf8ccac7369eb15c3e5fd4801e79d0c`,
with plugin 0.7.11 on both hosts. `engine setup --host devbox` completed
successfully before the fixture: tools, engine, plugin, configuration,
sign-in and Codex daemon checks passed, then both harness preflights passed.
The Claude session closed its window and completed every step without a
denied permission. The Codex session joined remote control, completed every
step, wrote hook state, and closed its window without a denied permission.

## Quota observed live

`engine ps` on the Mac, before fixture dispatch:

```text
claude  5h 19% (resets 17:30)  7d 70% (resets Thu 17:00)
codex  5h 95% (resets 17:32)  7d 51% (resets Mon 07:10)
```

The displayed times use the Mac's local zone (Europe/London). The complete
[table](evidence/ps-before.txt) and [JSON](evidence/ps-before.json) were
captured separately, so quota readings may advance between calls. JSON is
an object with `quota` and `sessions`; both harnesses have `five_hour` and
`seven_day` usage and epoch-second reset times. This proves both live quota
sources, rather than injecting a reading into a run directory.

The filtered host replies immediately before the run are
[mac](evidence/mac-start.json) and [devbox](evidence/devbox-start.json).
They retain engine identity, capacity, quota and window state; omit screen
contents, paths and host addresses. They show one live recorded Codex
session (this executor), no live recorded Claude sessions, five free slots
on Mac and three on devbox. The outer coordinator's window has no run and
counts as no harness session.

## Fixture

Feature [engine#148](https://github.com/dinoderek/engine/issues/148) has
three independent regression-test tasks and a final evidence task blocked
by all three. Every card leaves its harness unpinned. `engine lint dinoderek/engine#148` passed with four tasks. The content is the low-risk
open question delegated to this acceptance executor.

```sh
engine run dinoderek/engine#148 --harness-ratio claude=1,codex=1
```

The initial fleet had Claude=0 and Codex=1 live recorded sessions, including
this acceptance executor. At ratio 1:1, each score is `(live + 1) / 1`.
Placement increments those counts for each start in the same pass:

| Task | Start UTC / host / harness | Scores before start (Claude, Codex) | Reason | Result |
| --- | --- | --- | --- | --- |
| #149 output errors | 13:35:33 / mac / Claude | 1, 2 | Lower Claude score; Mac had 5 free slots versus devbox 3 | Merged PR #153 at 13:42 UTC |
| #150 quota isolation | 13:35:36 / mac / Claude | 2, 2 | Tie prefers `default_harness=claude`; Mac had 4 free slots versus devbox 3 | Merged PR #154 at 13:44 UTC |
| #151 quota retention | 13:35:41 / devbox / Codex | 3, 2 | Lower Codex score; both hosts had 3 free slots, so devbox wins on fewer capabilities | Stopped on a real limit before claim; continued manually at 19:51 UTC |
| #152 acceptance | Pending | Pending | Pending | Pending |

All three ready tasks were actually dispatched, including Codex while quota
was exhausted: quota does not gate starts. PR #153 adds writer-error
regressions; PR #154 adds independent fleet-merge and copied-output tests.
The coordinator reported #151 as a pre-claim stall at 13:41:03 UTC, without
a captured screen, rather than observing a limit. At 19:51 UTC I inspected
its devbox pane, selected “Keep current model”, verified GPT-6.1-Sol medium,
and sent literal `continue` then a separate Enter. This intervention did not
change a pin, cap, model or harness; it was manual recovery.

Pending: #151 and #152 merged PRs, coordinator exit and cleanup.

## Objective evidence

All nine required lanes of `./dev test` passed at 13:31 UTC against the
tested implementation commit. The final post-fixture run is recorded below
when the combined main is merged into this worktree.

| Objective | Evidence | Kind |
| --- | --- | --- |
| O1: both quota windows, expiry, fleet merge, table and JSON | Live captures above; `internal/hook/statusline_test.go` (`TestStatusline`, `TestStatuslineWritesOnlyChangedNumbersAndRenames`); `internal/hostops/codexquota_test.go` (`TestCodexQuotaMapping`, `TestStatusCodexQuota`, `TestCodexQuotaExchange`, process and deadline tests); `internal/session/quota_test.go` (`TestQuotaMergeAndExpiration`, `TestReadQuotaAcrossAllRuns`); `internal/ps/quota_test.go` (`TestListQuotaAcrossHostsWithoutClaims`); `internal/run/quota_test.go` (`TestFleetQuota`) | Live + unit/fake boundary/real process |
| O2: Claude and Codex limited states, reset, busy precedence, never stalled — **live Codex failure** | `internal/hook/limit_test.go` (`TestLimitStopsAndResetFallbacks`, `TestLimitEventsKeepOrClear`), including logged full hook input; `internal/hostops/limit_test.go` (`TestClaudeLimitStatus`); `internal/hostops/codexlimit_test.go` (`TestCodexLimitStatus`); `internal/ps/limit_test.go` (`TestLimitedRowsAndResetRendering`); `internal/run/limit_test.go` (`TestALimitedSessionNeverStalls`, `TestLimitedChangesTriggerACheck`) | Unit/fake boundary scenarios |
| O3: timed, idempotent recovery and escalation — **live Codex failure** | `internal/run/resume_test.go`: `TestResumeOncePerReset` (limited and waiting; no early typing; new reset; restart), `TestResumeStuckAlertOnce`, `TestFarResetAlertOnceThenResumeWhenDue`, `TestResumeLeavesOtherSessionsAlone`, `TestResumePreclaimAndPlanner`, `TestResumeFailureDoesNotPasteTwice`, `TestObservedLimitSurvivesTheQuotaReset`; `internal/run/resume_system_test.go` proves local/SSH literal text then separate Enter and refuses changed or unreadable windows | Fake boundary scenarios |
| O4: ratio choice and override, pinned/default behavior | Live fixture below; `internal/run/ratio_test.go` (`TestRatioStartsInOrderAndCountsPinnedCards`, `TestRatioLiveLoadFallbackAndDefault`, `TestRatioWaitsOnlyAfterAllChoicesFail`, `TestApplyPassesTheSelectedHarnessToStart`); `internal/cli/run_test.go` (`TestRunRatioArguments`, `TestRunRefusesInvalidRatioBeforeReadingTracker`); `internal/hosts/ratio_test.go` (`TestHarnessRatioConfig`) | Live + unit/command line/fake boundary |
| O5: coordinator skill matches O2–O4 inside budget | Read `plugins/engine/skills/engine/references/coordinate.md` against `internal/run/ratio.go`, `limit_test.go`, `resume.go`, and `resume_test.go`; `docs-check` passed at 794/800 words and enforces the existing word budget | Manual consistency review + gate |

Scenario tests use fixed clocks and fake hosts; they do not establish that a
real harness emitted a limit event. Hooks and status are covered at their
own boundaries, with the coordinator consuming the resulting state.

## Not proved

The feature's supervisor recorded real Codex quota stalls in engine#132 and
engine#137 before this feature's implementation shipped: the sessions stopped
before claiming and remained at the “Approaching rate limits” reminder after
the displayed reset. The supervisor selected “Keep current model”, sent
`continue`, then a later Enter, and verified that both sessions worked again.
[Supervisor evidence](https://github.com/dinoderek/engine/issues/131#issuecomment-6436507754)
and [recovery verification](https://github.com/dinoderek/engine/issues/131#issuecomment-6436534781)
record the intervention; the pre-claim messaging gap is engine#140.

Those earlier stops do not prove the new implementation: no `limit.json` or
`limits.jsonl` exists in the feature's implementation run directories on Mac,
and devbox has no such implementation runs. Codex Stop hooks carry ordinary
wait signals rather than a limit error; its new limited state is derived
from quota. A hook input with a real limit error and an automatic coordinator
resume are therefore not proved by these earlier events. O2/O3 are proved by
the boundary and scenario tests listed above. No real limit is manufactured.

## Actual limit failure on the implementation under test

This executor hit a real usage limit while running the new implementation.
The selected native rollout events are retained in
[real-limit-events.jsonl](evidence/real-limit-events.jsonl). This is actual
session output, filtered to the relevant quota fields and failed turn:

- `2026-10-27T13:33:11.670Z`: Codex five-hour usage 100%, reset
  `1793122362` (16:32:42 UTC, 17:32:42 local).
- `2026-10-27T13:33:11.995Z`: `event_msg/task_complete`,
  `error.codex_error_info=usage_limit_exceeded`, and the usage-limit retry
  message. There was no completed assistant message.
- The supervisor observed `harness-state.json` still saying `working`,
  `PostToolUse`, at `2026-10-27T13:33:11.668825Z`, immediately before the
  failed turn. At 19:45 UTC, `engine ps --json` still reported working,
  after quota reset to 2%; the coordinator had recorded neither limit nor
  resume. These pre-retry observations are sourced from the
  [supervisor's report](https://github.com/dinoderek/engine/issues/155), not
  reconstructed as a raw state-file snapshot: retry overwrote the file.
- The [pane excerpts](evidence/real-limit-screen-excerpts.txt) retain the
  usage error and model-switch reminder. The supervisor selected “Keep
  current model” and relayed the operator's retry at 19:47 UTC. The
  [post-retry JSON](evidence/ps-after-manual-retry.json) is a fresh reading,
  not evidence that detection or recovery worked before retry.

`internal/hostops/codexstate.go` returns the persisted `working` state before
considering quota. `internal/hook/state.go` sets it on `PostToolUse`; the
failed request supplied no later Stop/Interrupt state change. Thus the
coordinator had no limit reset to retain and no eligible recovery action.
The existing `TestCodexLimitStatus/working_wins` checks exactly the designed
precedence; the real stop invalidates the assumption that working hook state
must still mean active work.

The [official hook documentation](https://learn.chatgpt.com/docs/hooks)
lists no `StopFailure` event; its Stop input has no limit error or reset
fields. The native failed-turn event offers additional evidence, but using
it as a state source and handling the reminder requires a design decision
beyond this acceptance card's test-only fixture. No product fix is smuggled
into the evidence PR, and no automatic recovery is claimed.

Pending: final fixture and limit-history audit. The evidence remains a failed
live acceptance for O2/O3 until an approved fix is verified.

## Investigated fix boundary

A read-only `thread/turns/list` query through a short-lived Codex app-server
in engine's Codex home returned the same failed turn and
`codexErrorInfo=usageLimitExceeded`; the
[filtered reply](evidence/app-server-turns.json) keeps only turn status,
error and times. It sent no model prompt and did not resume the thread.
This demonstrates an alternative to parsing native rollout files. The
[official app-server documentation](https://learn.chatgpt.com/docs/app-server)
describes stored-turn reads without resuming and failed turns carrying errors.

Recommendation for the operator: use a newer failed usage-limit turn as
explicit limit evidence, with observed quota reset retained for recovery;
never let an old failed turn override a newer submitted turn. A separate
reader's `notLoaded`/hydrated runtime state cannot prove that this embedded
session is inactive, so summary state alone is insufficient. Suppress the
model-switch reminder only for engine's unattended launches via the documented
`notice.hide_rate_limit_model_nudge` setting, preserving the selected model.
The [configuration reference](https://learn.chatgpt.com/docs/config-file/config-reference)
defines that opt-out. Existing manual sessions and the operator's Codex home
would stay outside such a change.

These are proposed design changes for engine#155, not implemented behavior.
The live acceptance cannot be marked passed or auto-merged while the observed
O2/O3 failure is unresolved without an operator decision.
