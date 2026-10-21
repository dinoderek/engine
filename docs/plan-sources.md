# Plan sources

> **Owns:** how each plan source stores features, cards, dependencies and state, operation by operation and field by field, and who holds each mapping. **Not here:** what the operations and card fields mean (`execution-protocol.md`), how the planner writes them (`planner.md`).

A feature's plan lives in exactly one source: the **tracker** (GitHub Issues),
which the coordinator drives, or an **in-repo plan**, which the operator runs
one task per session with no coordinator.

**One place per field.** Within a source, every card field lives in exactly
one place. Where the source has a native field, such as GitHub's "blocked by",
the card field lives there and never also in the text, so the two cannot
drift.

## Who holds the mapping

| Source | Mapping held by | Implemented by |
| --- | --- | --- |
| GitHub | This doc. It is fixed: every repo stores its plan the same way. | The coordinator's GitHub adapter, and the planner. |
| In-repo plan | The target repo, because plan layouts differ. Its plans doc carries an **engine binding**: a table from each engine field to the repo's heading or column (`project-context.md`). Without one, the default layout below applies. | The executor, which reads the binding at load. |

> **Q10.** Should the in-repo binding live in the target repo? *Recommend:
> yes.* BOGA3 already has its own milestone and card templates, and a repo's
> template changes with the repo, not with engine.

## Operations

| Operation | GitHub | In-repo plan (default layout) |
| --- | --- | --- |
| Feature | An issue labelled `engine:feature`, with the state label. | A milestone file. |
| Tasks | Sub-issues of the feature. | Card files, listed in the milestone's task table. |
| State | `engine:` labels. | The milestone task table's Status column. The PR that ships a card deletes it. There is no `running` or `needs-human`, because the operator is in the session. |
| Inbox | Issue comments. | The live session and the PR's comments. |
| `med` and `high` rulings, deviations, outcome | Issue comments, repeated in the PR body. | The PR body only. |
| Ruling review queue | The `engine:ruling-review` label. | The merged PRs' bodies. |
| PR link and close | `Closes #<task>` in the PR body. | The PR deletes the card and marks it `completed` in the milestone. |

Feature status at a glance on GitHub: the feature issue's state label and its
sub-issue progress. Other trackers plug in behind the same operations.

> **Q9.** Is this the right mapping for in-repo plans? *Recommend: yes.* It
> matches how `task-protocol` already runs BOGA3's `docs/plans`, so an in-repo
> card can run hands-off without moving it to the tracker.

## Card fields

| Field | GitHub | In-repo plan (default layout) |
| --- | --- | --- |
| Outcome, Decided, Open, Tests, Docs to update, Requires, Gates, Harness / tier | A `## <Field>` heading in the issue body. | A `## <Field>` heading in the card file. |
| Depends on | Native "blocked by" links. Never in the body. | The milestone task table's Depends on column. |
| Parent feature | Native sub-issue link. | The milestone's task table lists the card. |
| Card key | A hidden `<!-- engine:card <key> -->` in the body. | The card's ID, its file name. |

The coordinator and plan lint find fields by their exact heading, so heading
names are fixed strings.

## Example binding: BOGA3

How BOGA3's task-card template would map, as the binding in its plans doc
would state it:

| Engine field | BOGA3 |
| --- | --- |
| Outcome | `## Objective` |
| Decided | `## Decided` |
| Open | `## Open — resolve with the user at session start`. In an engine run, the executor decides these and grades each per "Rulings". |
| Tests | `## Deliverables and acceptance` |
| Docs to update | `## Specs to update` |
| Gates | `## Gates` |
| Depends on, state | The milestone's `## Task breakdown` table. |
| Requires, Harness / tier | Missing, and not needed: with no coordinator there is no placement, and the operator starts the session on the model they choose. |
