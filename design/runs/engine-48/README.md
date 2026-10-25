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
| #49 dies before claim | M1: this session executed it by hand, hands-off | 10:25:41 | 10:25:43 | 10:32:50 (#53) | 10:32:53 |
| #51 acceptance | M4: `engine run` started it, this session supervised | 10:33:25 | 10:33:55 | 10:40:32 (#54) | 10:40:38 |

`engine run dinoderek/engine#48` ran from 10:33:20 to 10:41:41 and exited 0.
It started #51, set `engine:verifying`, and when #54 merged it posted the
summary, set `engine:done` and closed #48 as completed. The supervisor made no
intervention during the run; afterwards it closed #51's window (below). #49 took the feature's merge lock (`engine:merging`) after #50
had merged, merged `main` in cleanly, and was reviewed again before merging.

The run directories of #50 and #51 hold no `permissions.jsonl`: neither
unattended session met a permission request. #49 ran in this session,
attended, so the hook was not active there. #50 closed its own window. #51
did not: it ended its report with "I'll do it as my final action once you've
seen this" and waited, with `close the tmux window` in its input box, most
likely Claude Code's suggested next prompt, unsent; nobody typed it. Both
released their worktrees. The supervisor closed #51's window at about 10:50.

What M3 exercised: the Claude coordinator ran `engine start` for #50 and
watched it with `engine ps`; `engine run` did the rest of the pass (starting
#51, the labels, the close). M1's executor was this same session, not a
separate one given the kickoff line.

## Objectives of engine#31

| # | Proved by | Not proved |
| --- | --- | --- |
| O1 one coordinator spec | `coordinate.md` (#41); `engine run` implements it (#46), its scenarios in `internal/run/scenario_test.go`; the live run above | |
| O2 run modes | M1, M3 and M4 live above (with the limits stated there); M4 and M5 by the fake-world scenarios in CI | M2, deferred by the operator; a live M5 run with no supervisor |
| O3 unattended permissions | Deny and log: `internal/hook/permission_test.go`, `internal/start/start_test.go`, a real `claude -p` run (#42); `engine start --check` passes | Raising the request on the issue: protocol text (`rulings.md` Permission) only, never exercised |
| O4 stalls with the screen | The `internal/run` stall scenarios | A live stall @mention: none happened. (#45's stalled screen was a preflight report, not a run's stall.) |
| O5 shared open questions | `internal/lint/lint_test.go` (#44); real `engine lint` on #31 found 9 gaps in its cards, #48 passed | |
| O6 merge lock and start order | `execute.md` (#41); the ordering scenario; #49 held the lock live after #50 released it | #51 merged without taking the lock, though `execute.md` asks for it (see Learnings) |
| O7 plain-word rulings | `rulings.md` (#41); PRs #41 to #46 use the `User notices:` and `Alternative:` lines | `ruling-review` for `high` only: no `high` ruling occurred |
| O8 budget headroom and close | `plan.md` (#41); `engine run` closed #48 with a summary | A Prepare headroom report: spec only, neither #31 nor #48 carries one (#32 trimmed docs instead) |

## Learnings

- **The checks caught real faults.** `engine start --check` caught a marker in
  `os.TempDir()` (outside `/tmp`) on its first run. `engine lint` caught
  missing `## Requires` and `## Tests` on #31's own cards.
- **Executors were fast:** claim to merge took 4 minutes for #50 and about
  6.5 for #51 on Sonnet, and 7 for #49 on Opus.
- **Gap: empty coordinator note dirs.** `engine run` makes a
  `<repo>-<n>-coordinator` directory for each open task it reads, even with
  no note to write (here `engine-51-coordinator`, empty).
- **Gap: the merge lock with no open sibling.** #51 merged without
  `engine:merging`. With no open sibling the lock guards nothing, but
  `execute.md` asks for it; either the executor takes it or the rule says a
  last task needs none.
- **Gap: the supervisor's own setup.** `engine run` ran as a background
  process here, not in a tmux window as `supervise.md` says.
- **Gap: a hands-off executor waited instead of closing its window.** #51
  (Sonnet) asked for acknowledgement before step 12's `kill-window`. The
  skill could say plainly that nobody will answer.
- **Gap: `engine run` leaves live windows at done.** It saw #51 waiting
  (`waiting-since` 10:41:35) and exited at 10:41:41: reaping needs 10 minutes
  of waiting, and the run ends at done. It could reap, or wait to reap,
  before it exits.
- **Review rounds.** The two-round cap forced deviations on reviews that were
  converging (#40 took four rounds, #41 ten); the operator replaced it (#47):
  loop until approval, and stop only on a disagreement or a change against
  the brief.
- **Next:** Claude on the devbox, and Codex on both machines.
