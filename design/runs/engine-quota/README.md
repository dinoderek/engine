# Run: quota, limit recovery and harness ratio (ephemeral)

Acceptance evidence for engine#131, executed by engine#138 on 2026-10-27.
The acceptance executor runs on `mac`; a separate `engine run` drives the
fixture engine#148 across `mac` and `devbox`.

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

Pending: actual starts, counts, merged PRs, coordinator exit and cleanup.

## Objective evidence

All nine required lanes of `./dev test` passed at 13:31 UTC against the
tested implementation commit. The final post-fixture run is recorded below
when the combined main is merged into this worktree.

| Objective | Evidence | Kind |
| --- | --- | --- |
| O1: both quota windows, expiry, fleet merge, table and JSON | Live captures above; `internal/hook/statusline_test.go` (`TestStatusline`, `TestStatuslineWritesOnlyChangedNumbersAndRenames`); `internal/hostops/codexquota_test.go` (`TestCodexQuotaMapping`, `TestStatusCodexQuota`, `TestCodexQuotaExchange`, process and deadline tests); `internal/session/quota_test.go` (`TestQuotaMergeAndExpiration`, `TestReadQuotaAcrossAllRuns`); `internal/ps/quota_test.go` (`TestListQuotaAcrossHostsWithoutClaims`); `internal/run/quota_test.go` (`TestFleetQuota`) | Live + unit/fake boundary/real process |
| O2: Claude and Codex limited states, reset, busy precedence, never stalled | `internal/hook/limit_test.go` (`TestLimitStopsAndResetFallbacks`, `TestLimitEventsKeepOrClear`), including logged full hook input; `internal/hostops/limit_test.go` (`TestClaudeLimitStatus`); `internal/hostops/codexlimit_test.go` (`TestCodexLimitStatus`); `internal/ps/limit_test.go` (`TestLimitedRowsAndResetRendering`); `internal/run/limit_test.go` (`TestALimitedSessionNeverStalls`, `TestLimitedChangesTriggerACheck`) | Unit/fake boundary scenarios |
| O3: timed, idempotent recovery and escalation | `internal/run/resume_test.go`: `TestResumeOncePerReset` (limited and waiting; no early typing; new reset; restart), `TestResumeStuckAlertOnce`, `TestFarResetAlertOnceThenResumeWhenDue`, `TestResumeLeavesOtherSessionsAlone`, `TestResumePreclaimAndPlanner`, `TestResumeFailureDoesNotPasteTwice`, `TestObservedLimitSurvivesTheQuotaReset`; `internal/run/resume_system_test.go` proves local/SSH literal text then separate Enter and refuses changed or unreadable windows | Fake boundary scenarios |
| O4: ratio choice and override, pinned/default behavior | Live fixture below; `internal/run/ratio_test.go` (`TestRatioStartsInOrderAndCountsPinnedCards`, `TestRatioLiveLoadFallbackAndDefault`, `TestRatioWaitsOnlyAfterAllChoicesFail`, `TestApplyPassesTheSelectedHarnessToStart`); `internal/cli/run_test.go` (`TestRunRatioArguments`, `TestRunRefusesInvalidRatioBeforeReadingTracker`); `internal/hosts/ratio_test.go` (`TestHarnessRatioConfig`) | Live + unit/command line/fake boundary |
| O5: coordinator skill matches O2–O4 inside budget | Read `plugins/engine/skills/engine/references/coordinate.md` against `internal/run/ratio.go`, `limit_test.go`, `resume.go`, and `resume_test.go`; `docs-check` passed and enforces the existing word budget | Manual consistency review + gate |

Scenario tests use fixed clocks and fake hosts; they do not establish that a
real harness emitted a limit event. Hooks and status are covered at their
own boundaries, with the coordinator consuming the resulting state.

## Not proved

Pending limit-history inspection at fixture completion. No real limit is
manufactured; a simulated test is never described as a live limit.
