# Run: engine#48, the run-mode fixture (ephemeral)

The acceptance evidence for engine#31 (run modes, unattended permissions and
the BOGA3#881 learnings). Fixture feature engine#48 has three small real
tasks, each run in a different mode, on 2026-10-25, host `mac`. Setup first:
`engine` built from `main` at 4d97e50 into `~/.local/bin`, plugin 0.7.1 at
user scope, then `engine start --check` passed (closed its window, every step
done, no request denied).

## Timeline (UTC)

| Task | Run mode | Started | Claimed | Merged | Report |
| --- | --- | --- | --- | --- | --- |
| #50 `engine tell -` | M3: a Claude coordinator (this session) ran `engine start` | 10:25:31 | 10:25:55 | 10:29:58 (#52) | 10:30:02 |
| #49 dies before claim | M1: this session executed it by hand, hands-off | 10:25:41 | 10:25:41 | 10:32:50 (#53) | 10:32:53 |
| #51 acceptance | M4: `engine run` started it, this session supervised | 10:33:25 | 10:33:55 | 10:40:32 (#54) | 10:40:38 |

`engine run dinoderek/engine#48` ran from 10:33:20 to 10:41:41 and exited 0.
It started #51, set `engine:verifying`, and when #54 merged it posted the
summary, set `engine:done` and closed #48 as completed. The supervisor made no
intervention. #49 took the feature's merge lock (`engine:merging`) after #50
had merged, merged `main` in cleanly, and was reviewed again before merging.

No run directory holds a `permissions.jsonl`: no session met a permission
request. Every hands-off window closed itself.

## Objectives of engine#31

| # | Proved by |
| --- | --- |
| O1 one coordinator spec | `coordinate.md` (#41); `engine run` implements it (#46), its scenarios in `internal/run/scenario_test.go`; the live run above |
| O2 run modes | M1, M3 and M4 live above; M4 and M5 by the fake-world scenarios in CI; M5's loop is the same code as the live M4 run. M2 (cloud sessions) is deferred by the operator |
| O3 unattended permissions | `internal/hook/permission_test.go`, `internal/start/start_test.go`; a real `claude -p` run denied and logged (#42); `engine start --check` passes |
| O4 stalls with the screen | `internal/run` scenarios; the first real `engine start --check` caught and printed a stalled screen (#45) |
| O5 shared open questions | `internal/lint/lint_test.go` (#44); real `engine lint` on #31 found 9 gaps in its cards, #48 passed |
| O6 merge lock and start order | `execute.md` (#41); the ordering scenario; #49 held the lock live |
| O7 plain-word rulings | `rulings.md` (#41); this feature's PRs use the new ruling lines |
| O8 budget headroom and close | `plan.md` (#41); #32 trimmed three full docs first; `engine run` closed #48 with a summary |

## Learnings

- **The checks caught real faults.** `engine start --check` caught a marker in
  `os.TempDir()` (outside `/tmp`) on its first run. `engine lint` caught
  missing `## Requires` and `## Tests` on #31's own cards.
- **Executors were fast:** claim to merge took 4 to 7 minutes on Sonnet.
- **Gap: empty coordinator note dirs.** `engine run` makes a
  `<repo>-<n>-coordinator` directory for every task it reads, even with no
  note to write.
- **Gap: the supervisor's own setup.** `engine run` ran as a background
  process here, not in a tmux window as `supervise.md` says.
- **Review rounds.** The two-round cap stopped convergent reviews; the
  operator replaced it (#47): loop until approval, and stop only on a
  disagreement or a change against the brief.
- **Next:** Claude on the devbox, and Codex on both machines.
