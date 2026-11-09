# Plan states: store intent, derive the rest

Ephemeral design. Status: **awaiting operator approval.**

## What the code does today

- `reconcile.planState` (`internal/reconcile/tasks.go`) recomputes
  `executing` / `blocked` / `verifying` every pass and, when it differs from
  the stored state, emits `SetState`. `controlrun.apply` writes it through
  `transition`, which **bumps the plan's version and appends an update** on
  every flip.
- The only reader of those three states is `planStep`
  (`internal/reconcile/decide.go`), as "planning is over"
  (`planned|executing|verifying|blocked|done`). `stateChange`
  (`internal/control/state.go`) lists the transitions. Nothing else reads
  them: not merges, dispatch, locks, lint, `ps` (tasks only), or any skill.
  The UI shows the stored string verbatim in the plan list, its `state`
  filter and the task-pane header.
- `blocked` today means only "an open task is `needs-human`". It ignores open
  plan questions and `failed` tasks.
- `verifying` means "only the acceptance task is open". Nothing acts on it.

Side effect worth killing: each flip bumps `version`, so an operator's or
planner's `edit --version n` can 409 because the coordinator flipped
`executing` ↔ `blocked`, and the plan's thread fills with state updates.

## New state machine

Stored plan states: `prepare` → `approved` (operator) → `planned` → `done`.

| State | Who moves it | Why it stays stored |
| --- | --- | --- |
| `prepare` | planner creates | intent: not yet approved |
| `approved` | operator (`plan approve`) | intent: approval, `ApprovedVersion` |
| `planned` | planner (`plan state`) or coordinator (`planStep`, lint passes, planner gone) | latch: planning is over; lint can later fail and must not reopen planning |
| `done` | coordinator, when every task is done | latch: ends coordination, emits the finished summary, refuses `run`; a task added later must not silently reopen it |

Removed: `executing`, `verifying`, `blocked`.

Task states are unchanged. I looked for other derivable ones:
`running` is set at dispatch but also by the executor on resume; `in-review`
records that a PR is open (external); `needs-human` / `failed` carry intent;
`todo` vs `approved` is the standalone/plan split. None is a pure function of
other records, so I propose no other task change.

## Derived plan status

One pure function in `internal/control` (the leaf every consumer already
imports):

```go
// PlanStatus is the plan's state as people read it: its stored state, or
// for an approved or planned plan, blocked when the operator is needed and
// executing once a task has started.
func PlanStatus(plan Item, tasks []Item, asked bool) string
```

- `blocked`: state is `approved` or `planned`, and `asked` (an unresolved
  question on the plan, via `PendingQuestion`) or any task is `needs-human`
  **or `failed`** (Q1).
- `executing`: state is `planned` and any task has left `todo`.
- otherwise: the stored state.

`verifying` is not derived (Q2): nothing acts on it, and the UI's task pane
already shows the acceptance task as the only open one.

Consumers:

- **Server** fills a read-only `status` on plan items in `get`, `list` and
  `plan-view` responses (`read` and `planView`, on the response copy). It is
  `json:"status,omitempty"` on `Item`, never set in a transaction, so never
  stored; a store test proves no record carries it (Q3).
- **CLI**: `engine plan show` / `list` print JSON, so they gain `status` for
  free. `engine ps` lists tasks only: unchanged.
- **UI**: plan list, `state` filter facets and the pane header use `status`.
  The query parameter keeps the name `state` so bookmarked URLs work (Q4).
  `verifying` leaves `app.js`'s labels.
- **Reconcile**: needs none of it. `planState` and its `SetState` go;
  `planStep` checks `planned|done`. `isAcceptance` stays only if something
  else uses it (it does not today), else it goes too.

## Migration

`internal/control/store.go` is at `user_version=1` and refuses anything
higher. Bump to 2. On open, inside the one `BEGIN IMMEDIATE`:

```sql
UPDATE records SET data = json_set(data, '$.state', 'planned')
 WHERE key LIKE 'item/%'
   AND json_extract(data, '$.kind') = 'plan'
   AND json_extract(data, '$.state') IN ('executing', 'verifying', 'blocked');
PRAGMA user_version = 2;
```

Version 1 → migrate; 2 → no-op; >2 → refused as today. Versions are not
bumped (the state is the same intent, and a version bump would 409 an
in-flight edit). Past updates whose text says `executing (version n)` are
history and stay. An old binary on a migrated database refuses it
("unsupported control database schema"), which is the right failure. The
devbox server migrates only when the operator upgrades it.

## Server transitions

`stateChange` allowed plan moves become `approved → planned`,
`planned → done`. `plan state` with a removed state is a 409
(`cannot move planned to executing`).

## Docs and skills

- `docs/execution-protocol.md` "States": the plan row, and "blocked" and
  "executing" as computed beside "Ready". Who moves what.
- `docs/control-server.md`: plan `status` on reads; `/api/plans` filters on
  status; the migration line on schema versions.
- `docs/coordinator.md`: the pass no longer moves plan states beyond
  `planned` and `done` (one line).
- `plugins/engine/skills/engine/references/server.md`, `plan.md`: confirm
  they name only `planned`; adjust if a supervisor step reads a plan state.
- Word budgets: net cut expected; no budget change.

## Tests

- **Unit** (`internal/control`): table for `PlanStatus`: every stored state,
  needs-human, failed, plan question, started vs all-todo, done with a stale
  needs-human.
- **Unit** (`internal/control`): `stateChange` rejects
  `planned → executing|verifying|blocked`; accepts `approved → planned`,
  `planned → done`.
- **Unit** (`internal/reconcile`): a pass with a needs-human task, and one
  with only the acceptance task open, emits no `SetState`; `planStep` still
  hands off at `planned`. Replace the existing planState cases.
- **Real process** (`sqlite3`): a v1 database with a plan in each removed
  state, plus a `planned` plan, a task and a repository, opens as v2 with
  those plans `planned` and every other record byte-identical; reopening is
  a no-op; v3 is refused.
- **Fake boundary / command line**: `get` / `list` / `plan-view` carry
  `status`; stored records never do.
- **UI**: `planList` filters and facets by status; `app.js` tests drop
  `verifying` and use `status`.

## Open questions (my recommendation first)

- **Q1. Does a `failed` task block the plan?** Recommend **yes**: only the
  operator can retry it, and the plan cannot finish without it. Today's
  stored `blocked` ignores it. Alternative: needs-human and questions only,
  matching today.
- **Q2. Derive `verifying` or drop it?** Recommend **drop**: no code acts on
  it and the task pane shows the same fact. Alternative: derive it in
  `PlanStatus` for display.
- **Q3. Where does `status` travel?** Recommend a response-only `status`
  field on plan items. Alternative: a separate `statuses` map on
  `Response`, which keeps `Item` purely stored but every client must join it.
- **Q4. UI filter parameter.** Recommend keeping `?state=` with status
  values. Alternative: rename to `?status=`.
- **Q5. Overlap with the plan-questions session.** `PlanStatus` reads
  questions only through `PendingQuestion`, so whichever lands second merges
  one call site. If their resolve-restores-task-state changes how a plan
  question parks tasks, `blocked` follows automatically.
