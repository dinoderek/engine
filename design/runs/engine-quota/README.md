# Run: quota, limit recovery and harness ratio (ephemeral)

Acceptance evidence for engine#131, executed by engine#138 on 2026-10-27 and 2026-10-28.
The acceptance executor runs on `mac`; a separate `engine run` drives the
fixture engine#148 across `mac` and `devbox`.

## Acceptance status

The live fixture and both quota displays are verified. The original real
Codex limit **failed** O2/O3; that failure remains recorded below. The operator
approved a corrective design on October 28, and the fix now passes the real
failed-turn regression plus the recovery scenarios and all quality gates.
A new post-fix real usage limit and automatic resume have **not** been
observed; this record never counts the manual retry as automatic recovery.

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
| #151 quota retention | 13:35:41 / devbox / Codex | 3, 2 | Lower Codex score; both hosts had 3 free slots, so devbox wins on fewer capabilities | Stopped on a real limit before claim; continued manually at 19:51 UTC; merged PR #156 at 19:54:20 UTC |
| #152 acceptance | 19:55:04 / mac / Claude | 1, 2 | Previous fixture windows had closed; this executor remained Codex; Mac had more free slots | Merged PR #157 at 20:05 UTC |

The [selected coordinator events](evidence/fixture-events.txt) retain the
actual starts and stall; [placement inputs](evidence/placement-inputs.json)
retain the relevant non-sensitive configuration values.

All three ready tasks were actually dispatched, including Codex while quota
was exhausted: quota does not gate starts. PR #153 adds writer-error
regressions; PR #154 adds independent fleet-merge and copied-output tests.
The coordinator reported #151 as a pre-claim stall at 13:41:03 UTC, without
a captured screen, rather than observing a limit. At 19:51 UTC I inspected
its devbox pane, selected “Keep current model”, verified GPT-6.1-Sol medium,
and sent literal `continue` then a separate Enter. This intervention did not
change a pin, cap, model or harness; it was manual recovery.

PR #156 adds status-line retention regressions and merged as
`56a96b69c29ae02fb92a667df8f4453aeab82fb8`. The final fixture task merged
as PR #157 (`a204da2`), recording all three test objectives in
`design/runs/quota-regression-fixture/README.md`. The coordinator reached
`done` at 20:06:44 UTC and closed feature #148; all four task issues are
closed. Its original process exit code was not retained after the interrupted
session, so no exit-code claim is made. Final host snapshots below verify
that the four fixture windows are gone.

## Objective evidence

All nine required lanes of `./dev test` passed at 13:31 UTC against the
tested implementation commit. A second complete run at 19:53 UTC also passed after merging fixture PRs
#153 and #154. [Its summary](evidence/gates-1953.md) is retained.
After merging all three test PRs, the complete `./dev test` passed again at
19:54 UTC: all nine lanes, including race tests and coverage. Its
[summary](evidence/gates-1954.md) is retained. Post-fixture and corrective gates also pass, as recorded below. The original
live failure remains a failure.

| Objective | Evidence | Kind |
| --- | --- | --- |
| O1: both quota windows, expiry, fleet merge, table and JSON | Live captures above; `internal/hook/statusline_test.go` (`TestStatusline`, `TestStatuslineWritesOnlyChangedNumbersAndRenames`); `internal/hostops/codexquota_test.go` (`TestCodexQuotaMapping`, `TestStatusCodexQuota`, `TestCodexQuotaExchange`, process and deadline tests); `internal/session/quota_test.go` (`TestQuotaMergeAndExpiration`, `TestReadQuotaAcrossAllRuns`); `internal/ps/quota_test.go` (`TestListQuotaAcrossHostsWithoutClaims`); `internal/run/quota_test.go` (`TestFleetQuota`) | Live + unit/fake boundary/real process |
| O2: Claude and Codex limited states, reset, busy precedence, never stalled — original live failure; corrective tests below | `internal/hook/limit_test.go` (`TestLimitStopsAndResetFallbacks`, `TestLimitEventsKeepOrClear`), including logged full hook input; `internal/hostops/limit_test.go` (`TestClaudeLimitStatus`); `internal/hostops/codexlimit_test.go` (`TestCodexLimitStatus`); `internal/ps/limit_test.go` (`TestLimitedRowsAndResetRendering`); `internal/run/limit_test.go` (`TestALimitedSessionNeverStalls`, `TestLimitedChangesTriggerACheck`) | Unit/fake boundary scenarios |
| O3: timed, idempotent recovery and escalation — original live failure; corrective tests below | `internal/run/resume_test.go`: `TestResumeOncePerReset` (limited and waiting; no early typing; new reset; restart), `TestResumeStuckAlertOnce`, `TestFarResetAlertOnceThenResumeWhenDue`, `TestResumeLeavesOtherSessionsAlone`, `TestResumePreclaimAndPlanner`, `TestResumeFailureDoesNotPasteTwice`, `TestObservedLimitSurvivesTheQuotaReset`; `internal/run/resume_system_test.go` proves local/SSH literal text then separate Enter and refuses changed or unreadable windows | Fake boundary scenarios |
| O4: ratio choice and override, pinned/default behavior | Live fixture above; `internal/run/ratio_test.go` (`TestRatioStartsInOrderAndCountsPinnedCards`, `TestRatioLiveLoadFallbackAndDefault`, `TestRatioWaitsOnlyAfterAllChoicesFail`, `TestApplyPassesTheSelectedHarnessToStart`); `internal/cli/run_test.go` (`TestRunRatioArguments`, `TestRunRefusesInvalidRatioBeforeReadingTracker`); `internal/hosts/ratio_test.go` (`TestHarnessRatioConfig`) | Live + unit/command line/fake boundary |
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
wait signals rather than a limit error; the corrective reader supplements
quota with stored failed-turn evidence. A hook input with a real limit error and an automatic coordinator
resume are therefore not proved by these earlier events. The listed tests cover the intended boundary and coordinator scenarios;
live O2/O3 acceptance failed as recorded below. No real limit is manufactured.

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

In the original build, `internal/hostops/codexstate.go` returned the persisted
`working` state before considering quota. `internal/hook/state.go` sets it on `PostToolUse`; the
failed request supplied no later Stop/Interrupt state change. Thus the
coordinator had no limit reset to retain and no eligible recovery action.
The `TestCodexLimitStatus/working_wins` scenario covers working without an
explicit failed-turn reading; the recorded stop exposed the missing source.

The [official hook documentation](https://learn.chatgpt.com/docs/hooks)
lists no `StopFailure` event; its Stop input has no limit error or reset
fields. The initial gpt-6.1-sol review therefore required a design decision.
The operator approved continuing with the proposed fix, recorded in the
task ruling; the original failed limit was not retroactively recovered.

## Investigated fix boundary

A read-only `thread/turns/list` query through a short-lived Codex app-server
in engine's Codex home returned the same failed turn and
`codexErrorInfo=usageLimitExceeded`; the
[filtered reply](evidence/app-server-turns.json) keeps only turn status,
error and times. It sent no model prompt and did not resume the thread.
This demonstrates an alternative to parsing native rollout files. The
[official app-server documentation](https://learn.chatgpt.com/docs/app-server)
describes stored-turn reads without resuming and failed turns carrying errors.

The operator-approved design uses a newer failed usage-limit turn as
explicit limit evidence, with observed quota reset retained for recovery;
never let an old failed turn override a newer submitted turn. A separate
reader's `notLoaded`/hydrated runtime state cannot prove that this embedded
session is inactive, so summary state alone is insufficient. Suppress the
model-switch reminder only for engine's unattended launches via the documented
`notice.hide_rate_limit_model_nudge` setting, preserving the selected model.
The [configuration reference](https://learn.chatgpt.com/docs/config-file/config-reference)
defines that opt-out. Existing manual sessions and the operator's Codex home
would stay outside such a change.

## Corrective implementation and verification

The operator's “continue” answered the concrete design approval request.
The medium-risk ruling on #138 ends `Decided by: operator`. The corrective
code addresses #155 in this acceptance branch:

- Host status reads only known live engine Codex threads, in one bounded
  batch per home, and asks only for the newest stored turn without items.
  A failed `usageLimitExceeded` turn newer than the hook can mark a stale
  working state limited. Missing history keeps the hook result.
- Hooks retain optional `turn_id`, so a newer submitted turn prevents a
  same-second old failure from overriding it. Legacy state files remain
  readable. The reader checks engine's home, then the embedded home for
  missing threads; dead, ended and unrelated windows are excluded.
- Observed reset notes survive quota expiry even when the failed turn still
  establishes limited state. Unknown resets remain unknown and are not
  automatically resumed. Existing once-per-reset and escalation rules apply.
- Only hands-off launch arguments set
  `notice.hide_rate_limit_model_nudge=true`. The model remains selected by
  host configuration; gated and manually launched sessions keep their notice.

`internal/hostops/codexturns_test.go` includes
`TestRecordedCodexUsageFailure`, replaying the actual stored failure against
its last PostToolUse timestamp. It fails if that recorded usage error stays
working, and rejects the same old failure after newer work. Other tables
cover same-second turn identity, newer failed turns, incomplete history,
expired/unknown quota, unavailable history and the embedded-home fallback.
`TestCodexHistoryReadsOnlyLiveEngineThreads` protects query scope.

`internal/hostops/codexturns_process_test.go` verifies read-only request
construction, partial results and timeout cleanup with a real subprocess.
`TestFailedTurnLimitKeepsResetAfterQuotaExpires` in `internal/run/resume_test.go`
proves retained-reset resume and the later stuck-limit alert. Real-process
launch tests verify both joined and embedded hands-off arguments, and that
gated sessions retain their reminder. These are regression/scenario tests,
not a newly induced live limit.

All nine lanes passed at commit `02b0d7a`; fresh code review at `17d3039`
returned APPROVED, and its optional mixed-window test was added. Final
review and live two-host revalidation are recorded below.

## Post-fix host validation

The gated fix at `02b0d7a5772f` was installed using the toolchain pinned by
`go.mod`. `engine setup --host devbox` brought devbox to that commit with
plugin 0.7.11, and both harness preflights passed. The
[filtered setup report](evidence/setup-fixed.txt) retains its results while
omitting the host address and home-directory paths. The
[Mac](evidence/mac-fixed-final.json) and
[devbox](evidence/devbox-fixed-final.json) replies carry the same fix commit
and protocol. The devbox Codex preflight joined remote control, wrote state,
closed its window and had no denied permission request.

Fresh [table](evidence/ps-fixed.txt) and [JSON](evidence/ps-fixed.json) captures
show both live quota sources after the fix. The sessions array reports this
executor waiting, without a limit; that is the hook state observed during
the call, not a claim that its old failed turn recovered automatically.
Both host snapshots show the four fixture windows have been released.

The post-fix account is below its limit. A second real limit, a limit error
hook input from Codex, and a new automatic resume remain **not proved live**.
Codex supplies no such hook error; its failed-turn input is captured above
and replayed by `TestRecordedCodexUsageFailure`. The corrected failure path
and subsequent recovery timing are proved at their assigned test boundaries,
with the original failed live run and manual intervention retained.
