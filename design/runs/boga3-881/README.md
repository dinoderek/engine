# Run: BOGA3#881, muscle groups (ephemeral)

The first feature run through engine, hands-off, on 2026-10-24. There was no
`engine run` yet: a Claude Code session acted as the coordinator, with a
self-paced `/loop` that called `engine ps` and `engine start`. Learnings and
proposed next steps are in `learnings.md`.

## Raw data

Kept off git, because this repo is public and pane logs hold whatever a
session printed. Local to the operator's Mac, mode `go-rwx`:

```
~/.local/share/engine/archive/BOGA3-881/
  runs/<run-id>/{run.json,pane.log}   # engine start's run records and tmux pane logs
  transcripts/BOGA3-<n>/               # Claude Code session transcripts, with reviewer subagents
  transcripts/coordinator/             # the coordinator session
  metrics.json                         # the per-session numbers below
```

The run directories are still at `~/.local/share/engine/runs/` too. Scanned for
GitHub tokens on archiving: none found.

## Setup

- Host: `mac` only, `max_sessions = 3` (6 of 7 cards require `ios`).
- Plugin 0.3.1 for the first five sessions, 0.3.2 for #886 and #888.
- Feature labels: `prepare` → `approved` (operator) → `planned` (lint by
  hand) → `executing` → `verifying` → `done`, all set by the coordinator
  session.

## Timeline (UTC)

| Task | Tier / model | Started | Claimed | PR open | Merged | Claim → merge |
| --- | --- | --- | --- | --- | --- | --- |
| #889 storage and sync | deep / opus | 13:07 | 13:08 | 13:39 (#891) | 13:45 | 37 min |
| #883 transform, facts | standard / sonnet | 13:08 | 14:26 | 14:51 (#892) | 14:56 | 30 min, after a 78 min stall |
| #887 editor, search | deep / opus | 15:05 | 15:06 | 15:58 (#893) | 16:04 | 58 min |
| #884 settings | standard / sonnet | 15:05 | 15:06 | 16:07 (#894) | 16:32 | 86 min |
| #885 progress | standard / sonnet | 15:05 | 15:06 | 17:26 (#896) | 17:32 | 146 min |
| #886 session views | standard / sonnet | 16:08 | 16:09 | 16:43 (#895) | 17:00 | 51 min |
| #888 acceptance | deep / opus | 17:42 | 17:43 | 18:58 (#898) | 19:04 | 81 min |

Feature wall time: 13:07 → 19:05, about 6 hours. Critical path: #883 (with its
stall) → #885 → #888. #886 was ready at 14:56 and waited 72 minutes for a slot.

## Effort

Main session plus its reviewer subagents. Reviewers ran on opus throughout.

| Task | Tier | Turns | Tool calls | Reviewer runs | Output tokens | Cache read | Cache write |
| --- | --- | --- | --- | --- | --- | --- | --- |
| #883 | standard | 168 | 88 | 3 | 148k | 28.5M | 1010k |
| #884 | standard | 203 | 98 | 4 | 237k | 38.1M | 1067k |
| #885 | standard | 286 | 157 | 3 | 328k | 89.6M | 1408k |
| #886 | standard | 146 | 82 | 2 | 118k | 25.1M | 638k |
| #887 | deep | 244 | 125 | 4 | 319k | 55.1M | 1158k |
| #888 | deep | 314 | 158 | 2 | 266k | 74.3M | 893k |
| #889 | deep | 294 | 157 | 3 | 156k | 52.4M | 759k |
| **Total** | | 1655 | 865 | 21 | 1.57M | 363M | 6.9M |

## Outcomes

- 7 PRs merged, 6,712 lines added. #891 (storage and sync) added 3,107,
  over the plan's ~2,000-line aim.
- 8 rulings, all `med`, on 6 of 7 tasks. Two were operator rulings relayed
  mid-task: the docs/product budget (#883) and presets leaving tracking alone
  (#884).
- Acceptance proved O1–O8 and fixed one gap it exposed (group searches).
- Hard stops: none raised as `needs-human`. Operator input arrived through
  three other routes instead (see `learnings.md`, "Operator in the loop").
