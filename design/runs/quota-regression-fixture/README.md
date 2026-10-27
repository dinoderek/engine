# Run: the quota regression fixture (ephemeral)

The acceptance record for fixture feature engine#148: three test-only tasks
(#149, #150, #151) and this acceptance task (#152), run by
`engine run --harness-ratio claude=1,codex=1` with no harness pins, on
2026-10-27. Hosts, from the claim comments: `mac` for #149, #150 and #152,
`devbox` for #151. Which harness ran each task is recorded by the parent
acceptance executor (engine#138), not here.

## Timeline (UTC)

| Task | Claimed | Merged | PR | Merge commit | Test file (only file changed) |
| --- | --- | --- | --- | --- | --- |
| #149 ps output errors | 13:36:03 | 13:42:06 | #153 | c086691 | `internal/ps/render_errors_test.go` (+112) |
| #150 fleet quota merge | 13:36:09 | 13:44:20 | #154 | e0bcf4f | `internal/session/quota_isolation_test.go` (+66) |
| #151 status-line retention | 19:51:01 | 19:54:20 | #156 | 56a96b6 | `internal/hook/statusline_retention_test.go` (+99) |

#151's session started at 13:35:41 (its run ID) but did not claim; `engine run` posted
its "has not claimed the task 5m0s after it started" @mention at 13:41:04.
The claim came at 19:51:01, under the same run ID. Each PR's `gates` check
passed, and each changed only its test file: no product code, docs or gates.

## Gates on the combined main

`./dev test` on `main` at 56a96b6, which holds all three merges, started
2026-10-27T19:55:50Z: `Overall: PASS`, exit 0. Lanes passed: format, vet, lint,
unit (`go test -race -count=1 ./...`; `internal/hook`, `internal/ps` and
`internal/session` all `ok`), coverage (94.6% of 5841 statements, minimum
80%), file-length, imports, versions, docs-check.

## The tests against their cards

All three test files were read against their cards. Their tests were also run
alone, with `GOTOOLCHAIN` set from go.mod's toolchain line:

```sh
go test -count=1 -v -run 'TestWriteReturnsWriterError|TestWriteSucceedsOnHealthyWriter' ./internal/ps
go test -count=1 -v -run 'TestMergeQuotaKeepsHarnessesIndependentAcrossHosts|TestMergeQuotaReturnsCopiesOfWindows' ./internal/session
go test -count=1 -v -run 'TestStatuslineRetainsLastValidQuota' ./internal/hook
```

All 13 `internal/ps` results, both `internal/session` tests and all 11
`internal/hook` results passed. Then one temporary mutation per objective
went into the product code, and the tests were run against it. Every mutation
was reverted; afterwards `git diff origin/main -- internal` was empty.

| # | Card outcome | Covered by | Mutation (reverted) | Result |
| --- | --- | --- | --- | --- |
| O1 | `WriteJSON` and `WriteTable` return a sentinel writer error for quota lines, the no-claims line and the buffered row flush, checked with `errors.Is`; successful controls | `TestWriteReturnsWriterError`: 8 cases, each failing one chosen write (JSON with and without quota, first and second quota line, no-claims alone and after quota, row flush alone and after quota), checking `errors.Is` and what reached the writer. `TestWriteSucceedsOnHealthyWriter`: 3 healthy controls | `WriteTable` ignores `tw.Flush()`'s error | 2 subtests fail: `table_row_flush`, `table_row_flush_after_quota` |
| O2 | `MergeQuota` picks the newest reading per harness across hosts, expires windows at a fixed clock, keeps source timestamps and percentages, and returns copied windows | `TestMergeQuotaKeepsHarnessesIndependentAcrossHosts`: 3 hosts, Claude and Codex newest on different hosts, Codex's newest older than Claude's, resets at `now-1`, `now`, `now+1`, input order reversed. `TestMergeQuotaReturnsCopiesOfWindows`: changing, adding and deleting result windows leaves every input equal to a fresh copy | (a) the result shares the input's windows map; (b) one newest timestamp shared by both harnesses | (a) `TestMergeQuotaReturnsCopiesOfWindows` fails; (b) `TestMergeQuotaKeepsHarnessesIndependentAcrossHosts` fails |
| O3 | After a valid `quota.json`, absent limits, null or empty windows, invalid percentages or resets, and malformed JSON neither erase nor rewrite it; exact bytes and `observed_at`; the malformed-JSON error; normal output; a later changed reading replaces it | `TestStatuslineRetainsLastValidQuota`: 10 cases (9 unavailable inputs, 1 malformed), each starting from a real valid save, checking saved bytes, decoded reading, output or `io.ErrUnexpectedEOF`, then a changed valid reading replacing the bytes at a new `observed_at` | `Statusline` saves even when no window is valid | 9 of 9 unavailable-input subtests fail; the malformed case still passes, since decoding fails before any save |

Each sibling PR also records its own mutation runs: #153 seven,
#154 seven, #156 one.

## Objectives of engine#148

| # | Proved by | Not proved |
| --- | --- | --- |
| O1 ps output errors | #153; the O1 row above | |
| O2 independent harness quota merge, copied windows | #154; the O2 row above | |
| O3 last valid status-line quota retained | #156; the O3 row above | |
| O4 acceptance record | This file, with the gate run above on the combined main and the PR links | This record's own merge, which happens after it is written |

## Learnings

- **A stalled start claimed late.** #151 stayed unclaimed for about
  6 hours 10 minutes after the stall @mention. It then claimed on `devbox`,
  with no Remote Control, under the same run ID and `Started by: engine
  start`. This record does not show why the claim was late.
- **Executors were fast:** claim to merge took 6 minutes for #149, 8 for
  #150 and 3 for #151.
