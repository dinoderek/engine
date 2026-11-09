# Merge queue (design, batch 1)

Ephemeral. Replaces the executor-held repository merge lock with a merge
queue the coordinator owns inside `engine serve`. Open questions are marked
**Q<n>**, each with a recommendation.

## 1. Today, and what changes

Today the executor takes `engine lock acquire` (polling every 5 minutes),
refreshes, regates, re-reviews, then `engine task merge-ready`. The
coordinator calls the host's `merge` op, which refuses a PR behind its base,
red, or not mergeable. Refusals wake the executor; a held lock blocks the
repo until its owner releases it or the operator recovers it.

After this change:

- The executor runs `engine task merge-ready` once its PR is in-review,
  reviewed and pushed. It never touches a lock. It ends its turn and is woken
  by the queue.
- The coordinator keeps one queue per repository, picks one **head** at a
  time, merges it when its head commit is current and green, and tells the
  head's executor to refresh when it is not.
- Every wait in the queue has a fixed bound in code. When a bound expires the
  entry is demoted or dropped, and the executor and/or operator are told.

The executor still does every refresh (merge the default branch, gates,
fresh review, push): the coordinator never edits code.

## 2. Queue semantics

**Entry.** One per task with a live merge request. Created by
`merge-request`; a later request from the same task (a new head) updates the
entry and keeps its place. It belongs to the task, not the attempt: a
session that vanishes leaves its entry queued (section 6, rows 12-13).

**Order.** FIFO per repository across every plan and standalone task, by
`Seq`: the update sequence of the request that created the entry. A demotion
gives the entry a fresh `Seq` (the demotion update's sequence), which sends
it to the back.

> **Q1. Order.** FIFO, or the dispatch order (most open downstream tasks
> first, then tier)? *Recommendation: FIFO.* It is predictable, cannot starve
> anyone, and the operator can read it at a glance. Dispatch order already
> front-loads the critical path.

**Eligible.** An entry may become head only when: the task is `in-review`;
no unresolved question on it or its plan; not `needs-human`; its root is
coordinating; and it is not **held** (an unacknowledged host-failure notice,
row 9). Ineligible entries keep their `Seq` and are skipped, so they never
block the queue; once eligible again they take their place at the next head
change.

**Head.** At most one per repository. If an entry is already head it stays
head (no preemption) until it merges, leaves, or is demoted. Otherwise the
head is the eligible entry with the lowest `Seq`. Only the head's PR is
merged; at most one `merge` op per repository is in flight, because a pass
is single-threaded and only the head gets one.

**Head phases** (`Phase` on the entry):

| Phase | Means | Leaves when |
| --- | --- | --- |
| `check` | head, waiting for its PR to be current, green and mergeable | merged; or behind/conflict/head moved → `refresh`; or a bound expires |
| `refresh` | told to refresh (a `merge-turn` update woke the executor) | a newer request arrives (→ `check`); or `TurnWait` expires |

A non-head entry has phase `queued`.

**Non-head probes.** Every `ProbeEvery` the coordinator reads each queued
entry's PR without merging, so a closed, merged, red or blocked PR is told
within that bound instead of when it reaches the head. Behind and conflict
are ignored for non-heads: they refresh on their turn, once, not every time
the default branch moves.

> **Q2. Probe non-heads?** *Recommendation: yes, every 10 minutes.* Without
> it a red PR behind three heads is noticed hours late. The cost is one `gh
> pr view` per queued PR per 10 minutes on its host.

**Bounds** (constants in `internal/reconcile`, like `StallWait`):

| Constant | Value | Starts at | On expiry |
| --- | --- | --- | --- |
| `TurnWait` | 45 min | `merge-turn` (phase `refresh`) | demote |
| `ChecksWait` | 60 min | later of `HeadAt` and the newest request | demote |
| `MergeableWait` | 10 min | first `unknown` reading at this head | demote |
| `HostWait` | 15 min | first failed call (unreachable) at this head | demote |
| `GhFailures` | 3 consecutive | first gh error | hold + notify operator |
| `MaxDemotions` | 3 in a row | — | drop from the queue |
| `ProbeEvery` | 10 min | last probe | probe non-head |

A pass runs every 30 s, so "next pass" below means ≤ 30 s plus one host call.
Worst case one head occupies a repo for `TurnWait + ChecksWait` = 1 h 45 min
before the next entry proceeds.

> **Q3. The values.** The refresh is merge + `./dev test` + a fresh `deep`
> review + push; 45 minutes covers engine's own runs (`design/runs/`) with
> margin. *Recommendation: the values above, constants in code, no config.*

**Demote** = new `Seq` (back of the queue), phase `queued`, `Demotions++`,
a `merge-demoted` update that wakes the executor, and an operator notice.
**Drop** = the request is answered `merge-refused` with the reason, the entry
is deleted, executor woken, operator notified. A merge, or a new request
after a drop, resets `Demotions`.

## 3. Records and schema

**New record kind `queue/<task-id>`** (`control.QueueEntry`):

```go
type QueueEntry struct {
	Task      string    // task ID
	Repo      string    // repository ID
	Request   int       // newest merge-request update
	Seq       int       // order key
	Phase     string    // queued | check | refresh
	HeadAt    time.Time // became head
	TurnAt    time.Time // merge-turn sent (refresh)
	ProbedAt  time.Time // last read of its PR
	WaitSince time.Time // first unknown-mergeable reading at this head
	FailSince time.Time // first unreachable call at this head
	Failures  int       // consecutive gh errors
	Held      int       // the operator notice that holds it, until acked
	Demotions int       // consecutive demotions
}
```

Updates stay the history and the wake channel. New update kinds:
`merge-turn`, `merge-demoted`, `merge-withdrawn` (all three wake the
executor), plus the existing `merge-request`, `merged`, `merge-refused`.

> **Q4. A record, or derive from updates?** Merges today are derived from
> updates (`PendingMerge`). The queue needs a sticky head, timers and a
> demotion count; deriving those from history is possible but fragile and
> slow to read. *Recommendation: the explicit record*, with updates kept as
> history and wake-ups.

**Removed:** `State.Locks`, the `lock/` record kind, `control.Lock`,
`Request.Token`, `Response.Lock`, and the `lock-acquired`, `lock-released`
and `lock-recovered` update kinds for new writes (old updates stay as
history; they are plain updates).

**Schema version 1 → 2.** `Open` on a version-1 database migrates in one
`BEGIN IMMEDIATE` transaction, then sets `user_version=2`; a version-2
binary refuses anything newer, and a version-1 binary refuses version 2
("unsupported control database schema"), so a rollback needs the backup.
The migration:

1. **Pending merge requests → entries.** Each task's unanswered request from
   its current attempt becomes an entry with `Seq` = its sequence, phase
   `queued`.
2. **Lock rows are deleted.** For each, a `lock-retired` update on the
   holder's task says the lock is gone and the queue serializes merges.
3. **Every task in `running` or `in-review` with an active attempt** gets the
   same `lock-retired` notice (a wake kind), so an executor polling `engine
   lock acquire` learns to run `merge-ready` instead.

The coordinator's first pass picks heads from the converted entries.

> **Q5. Upgrade with locks held.** *Recommendation: migrate anyway as
> above*, and the PR's notes tell the operator to upgrade `devbox` when no
> merge is in progress (`engine ps` shows none in-review) and to take a
> backup first. The alternative, refusing to migrate while a lock is held,
> can block an upgrade on a dead session.

## 4. API and CLI

**`merge-request`** (`engine task merge-ready`, flags unchanged): same
checks minus the lock (worker of the current attempt, `in-review`, PR is the
task's, full head, method, `APPROVED`, no unresolved question). Creates or
updates the entry in the same transaction. A head request in phase
`refresh` moves it to `check`.

**Leaving `in-review` withdraws.** A task state change away from
`in-review` (to `running` to fix something, `needs-human`, `failed`) in the
same transaction answers the request `merge-withdrawn` and deletes the
entry. `needs-human` is the exception: a question holds the entry in place
instead (row 13), so set via `engine ask` it keeps its place.

> **Q6. needs-human keeps the place?** *Recommendation: yes for a question*
> (the operator answers, the task returns to `in-review`, and it is next at
> the following head change), *no for `running`*: the executor chose to
> rework, so it re-joins at the back.

**New read `merge-queue`** (`engine merge-queue <repo-id>`): the entries in
order with task, PR, head, phase, position, since, and the last reason.
Operator and workers of a task in that repository may read it (task IDs and
PR URLs only; no bodies).

**`engine task show <id>`** gains `queue`: the task's entry, if any.

**Removed:** ops `lock-acquire`, `lock-release`, `lock-check`,
`lock-recover`, `lock-show`; `engine lock …` and `--lock-token-file`; the CLI
writing `merge-lock.json`; `collect`'s exclusion of `merge-lock.json`
(no file exists any more); the dispatch kickoff's mention of locks; the
"Holding the merge lock" CLI hint.

> **Q7. A tombstone for `engine lock`?** *Recommendation: none.* The
> migration notice reaches every running executor; an unknown command
> already fails with the usage. A tombstone would need a doc line and a
> later removal.

**Host op `merge`** (protocol bump; hosts need `engine setup --host`):

- Its reply gains a structured `code`: `merged`, `pending-checks`,
  `pending-mergeable`, `behind`, `conflict`, `head-moved`, `red`, `closed`,
  `merged-elsewhere`, `blocked`. `outcome` and `reason` stay.
- It reads `mergeStateStatus` and `reviewDecision` too: `BLOCKED` with green
  checks, or `REVIEW_REQUIRED` / `CHANGES_REQUESTED`, is `blocked`. A `gh pr
  merge` error naming a branch policy is `blocked`; any other error stays an
  error.
- New flag `--check`: judge only, never merge (for non-head probes).

## 5. Reconcile View and actions

The queue spans plans, so it is decided once per repository per pass, before
the per-root passes, not inside `Decide(View)`.

```go
// QueueView is one repository's queue as a pass decides on it.
type QueueView struct {
	Now     time.Time
	Repo    string
	Entries []QueueItem // by Seq
	Fleet   Fleet
}
type QueueItem struct {
	control.QueueEntry
	Task     Item               // the task's record (question, state, coordinating root)
	Merge    control.Update     // the newest request
	Reply    *hostops.Merged    // the PR as last read this pass, nil when not read
	ReadErr  string             // the read failed: "unreachable" or the gh error
}

func DecideQueue(v QueueView) []Action
```

Two steps per pass, both pure and both tested on Views:

1. `DecideQueue` without replies picks the head (or keeps it) and the
   non-heads due a probe: actions `Merge` (head) and `Probe`.
2. `controlrun` makes those host calls, puts the replies into the View, and
   calls `DecideQueue` again, which maps each reply to transitions:
   `Merged`, `Turn`, `Demote`, `Drop`, `Hold`, `Wait` (records a
   `WaitSince`/`FailSince`/`Failures` change only), and `Notice`.

`controlrun` applies each transition in one service transaction that
rechecks the entry's `Request` and `Phase`, as `merge` rechecks today, so a
request or question that arrived during the host call wins. The merged
transition is today's: task `done`, PR recorded, request answered `merged`,
entry deleted, in one transaction.

`reconcile.Item` loses `Locked`; `MergeStep`'s refusals move into
`DecideQueue`. `stall` keeps skipping a task with an entry (it waits on the
coordinator). `merges(v)` leaves `Decide`.

## 6. Failure modes

"Executor" = woken by an update on its task (`merge-turn`, `merge-refused`,
`merge-demoted`, `merge-withdrawn`, `merged`). "Operator" = an inbox notice.
"Pass" = ≤ 30 s plus one host call.

| # | Failure | Detected by | Told | Bound | Place in queue |
| --- | --- | --- | --- | --- | --- |
| 1 | PR behind its base | head: `merge` op's compare (`behind`) | executor (`merge-turn`) | next pass; refresh within `TurnWait` | keeps head, phase `refresh`; demoted at `TurnWait` |
| 2 | Merge conflict | head: `mergeable=CONFLICTING` (`conflict`) | executor (`merge-turn`) | as row 1 | as row 1 |
| 3 | CI red (incl. flaky, after refresh) | head each pass, non-head within `ProbeEvery` (`red`) | executor (`merge-refused`, naming the check) | next pass (head), 10 min (non-head) | dropped; the executor fixes or reruns the check and requests again at the back |
| 4 | Checks pending a long time | head: `pending-checks` since the later of `HeadAt` and the request | executor and operator | `ChecksWait` (60 min) | demoted; dropped after `MaxDemotions` |
| 5 | Head moved after `merge-ready` | `head-moved`: PR head ≠ requested head | executor (`merge-turn`: request again for the new head after gating and review) | next pass; `TurnWait` | head: phase `refresh`, demoted at `TurnWait`; non-head: stays, told at its probe |
| 6 | PR closed | `closed` (head or probe) | executor and operator | next pass / 10 min | dropped |
| 7 | PR merged elsewhere | `merged` at the reviewed head → done as today; `merged-elsewhere` at another head | executor and operator | next pass / 10 min | removed (see Q8) |
| 8 | Branch protection or required review | `blocked` (merge state, review decision, or the merge error) | executor (`merge-refused`: ask the operator with `engine ask`) and operator | next pass / 10 min | dropped; re-requests after the operator's answer, at the back |
| 9 | Mergeability unknown | `pending-mergeable` | operator at expiry | `MergeableWait` (10 min) | demoted |
| 10 | `gh` sign-in broken on the host | the `merge` op returns an error (not a refusal), `GhFailures` in a row | operator, naming the host and `gh auth status` | 3 passes (≤ 2 min) | **held**: keeps `Seq`, skipped until the operator acks the notice; the next entry becomes head |
| 11 | Host unreachable | `UnreachableError` from the call | operator once at expiry | `HostWait` (15 min) | head demoted; never marked failed or dead (a failed call is unknown); the session is untouched |
| 12 | Server restarts mid-merge | the next pass calls `merge` again; the op reports a PR already merged at the reviewed head as `merged` | nobody needs to be; the merge completes as normal | first pass after start | unchanged; timers are wall-clock from persisted times |
| 13 | Executor session dies or vanishes while queued | existing session rules end the attempt `vanished` (task resumable) or `failed` | operator (existing vanished/failed notices) | existing (pass) | vanished: entry kept; if it reaches head and is current and green it merges with no session; a needed refresh waits for the resume that the existing vanish rule dispatches. failed: dropped |
| 14 | Executor dies while head, in `refresh` | as row 13 | as row 13, then row 1 at expiry | `TurnWait` regardless of resumes | demoted at `TurnWait`; the resumed executor reads `engine task show` (queue), refreshes and requests again |
| 15 | Unresolved question or `needs-human` on a queued task | `Question > 0` or state | already in the operator inbox | — (the operator's) | ineligible, keeps `Seq`; a head in either phase stops being head without demotion |
| 16 | Head of line: head does not refresh | phase `refresh` older than `TurnWait` | executor and operator | `TurnWait` | demoted; `MaxDemotions` in a row → dropped |
| 17 | Coordination stopped for the root | root not coordinating | nobody (the operator stopped it) | — | ineligible, keeps `Seq`; no merges for it while stopped |
| 18 | Task's host missing from `hosts.toml` | config lookup | operator | next pass | held, as row 10 |
| 19 | Executor sets the task `running` (rework) | state change | executor (`merge-withdrawn`) | immediate | removed; joins the back on its next request |
| 20 | A newer request while queued | `merge-request` | — | immediate | entry updated, keeps `Seq` |

> **Q8. Merged elsewhere at another head.** Today that is refused, which
> leaves the executor with a merged PR it cannot finish. *Recommendation:*
> record the task `done` with its PR and tell the operator that it merged at
> an unreviewed head, naming both commits. The alternative is a hard stop on
> the task.

> **Q9. Unreachable host: merge from another host?** The server has no
> GitHub credential, but any host's `gh` could merge. *Recommendation: no*;
> keep merges on the task's host, demote after `HostWait`. Residual risk: a
> call that timed out but merged on the host. The next read of that PR finds
> it merged at the reviewed head and completes it; meanwhile the new head's
> compare sees the base moved and turns it into a refresh, so nothing merges
> against a stale base.

## 7. Docs and skills that change

All within today's budgets (none raised):

| File | Change | Budget now |
| --- | --- | --- |
| `docs/coordinator.md` | "Merges and merge locks" → "Merge queue": semantics, head, bounds (cite the constants' file, not values), failure routing; header "merge locks" → "the merge queue"; drop the lock-token sentence | 1400 / 1400: must cut to fit |
| `docs/control-server.md` | queue entries replace locks in records; `engine merge-queue`; workers cannot recover locks → removed | 1489 / 1500 |
| `docs/execution-protocol.md` | "Merge conflicts": the queue's head refreshes; others wait in order | 890 / 900 |
| `docs/project-brief.md` | "repository merge locks" → "repository merge queues" | 449 / 450 |
| `docs/claude.md` | the section name it cites | — |
| `AGENTS.md` | load-on-demand row: "merge locks" → "the merge queue" | — |
| `plugins/engine/skills/engine/references/server.md` | step 10 rewritten: request once pushed and reviewed, end the turn; on `merge-turn` refresh and request again; on `merge-refused` fix and request again; `merge-demoted`/`merge-withdrawn` meanings; on resume read `engine task show` for the queue entry | 1200 |
| `plugins/engine/skills/engine/references/supervise.md` | "your lock is free" example → a queue example; `engine merge-queue` in Watch | 400 |
| `plugins/engine/skills/engine/SKILL.md` | "merge lock" → "merge queue" | 600 |
| both `plugin.json` | version bump | — |

Code text: the dispatch kickoff (`internal/controlrun/dispatch.go`), the CLI
hint (`internal/cli/control.go`), usage text (`internal/controlcmd`,
`internal/cli`, `internal/hostops`).

The `coordinator.md` cut: the "Reaching hosts" op table and envelope are
the densest text; I will tighten wording there and in "Session signals"
without dropping a rule. If that cannot fit, splitting the merge queue into
its own doc behind a load trigger is the fallback, which needs a new budget
row: a hard stop I would bring back to you.

## 8. Test plan (per `docs/testing-strategy.md`)

| Layer | Claims |
| --- | --- |
| Unit, `internal/reconcile` | `DecideQueue` on Views, table-driven with a fixed `Now`: head choice (FIFO, sticky head, skip ineligible, held, stopped root); one `Merge` per repo; probe cadence; every row of section 6 maps to its transition and notice, and each bound fires at exactly its constant and not one tick before; `MaxDemotions` drops; a merged head promotes the next in the same pass's second step. |
| Unit, `internal/hostops` | `judge` and the merge-error classifier yield each `code` from canned `gh` JSON: behind, conflict, red, pending checks, unknown mergeable, closed, merged at/away from head, blocked by `mergeStateStatus`, `reviewDecision` and by merge error text; `--check` never runs `gh pr merge`. |
| Fake boundary, `internal/controlrun` | a fake host client: unreachable keeps the session and entry (no end, no fail) and demotes at `HostWait`; gh errors hold after `GhFailures` and release on ack; the recheck transaction lets a question or newer request asked during the host call win; restart mid-merge (a fresh Coordinator on the same store) completes via "already merged"; a vanished head merges without a session; two repos each get one head in one pass. |
| Unit / service, `internal/control` | `merge-request` creates and updates entries, keeps `Seq`, needs no lock; leaving `in-review` withdraws (except `needs-human`); `merge-queue` and `task show` reads and their scoping (a worker of another repo is refused); lock ops are unknown. |
| Real process, `internal/control` | migration on a real `sqlite3` v1 database holding a lock row and a pending request: entry created, lock row gone, `lock-retired` notices posted, `user_version=2`; a v3 database is refused; a v2 binary reopens v2. |
| Command line, `internal/cli` | `engine merge-queue r1` output and exit codes; `engine lock acquire` fails as an unknown command; the help text no longer lists `lock`. |

Existing lock tests are removed with the code; the world/stack acceptance
tests in `internal/cli` move from lock-then-request to request-then-queue.

## 9. Open questions (summary)

| # | Question | Recommendation |
| --- | --- | --- |
| Q1 | Queue order | FIFO by first request |
| Q2 | Probe non-head PRs | Yes, every 10 min |
| Q3 | Bound values | `TurnWait` 45 m, `ChecksWait` 60 m, `MergeableWait` 10 m, `HostWait` 15 m, `GhFailures` 3, `MaxDemotions` 3; code constants |
| Q4 | Explicit queue record vs derived from updates | Explicit `queue/<task>` record |
| Q5 | Migrating while locks are held | Migrate (drop locks, convert requests, notify); upgrade when quiet, after a backup |
| Q6 | `needs-human` keeps the queue place | Yes for a question; rework (`running`) re-joins at the back |
| Q7 | Tombstone for `engine lock` | None |
| Q8 | PR merged elsewhere at an unreviewed head | Mark done, tell the operator |
| Q9 | Merge from another host when the task's is unreachable | No; demote after `HostWait` |
