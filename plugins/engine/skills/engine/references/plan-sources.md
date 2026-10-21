# Plan sources

A plan lives in exactly one source: **GitHub** issues, or an **in-repo plan**
of milestone and card files. Within a source, each field lives in exactly one
place. Where GitHub has a native field, such as "blocked by", the field lives
there and never also in the issue body.

## Which mapping applies

- **GitHub:** the columns below. They are the same in every repo.
- **In-repo plan:** read the repo's own card and milestone templates, and
  map their headings and columns to the engine fields by meaning. A repo's
  `Open` that says "resolve with the user" is still an open question you
  decide. Say in the PR body how you mapped the fields. The default in-repo
  column below applies when the repo has no template.

## Operations

| Operation | GitHub | In-repo plan (default) |
| --- | --- | --- |
| Feature | An issue labelled `engine:feature`, with the state label. | A milestone file. |
| Tasks | Sub-issues of the feature. | Card files, listed in the milestone's task table. |
| State | `engine:` labels. | The milestone task table's Status column. The PR that ships a card deletes it. There is no `running` or `needs-human`: the operator is in the session. |
| Claim | `engine:running` plus the claim comment (`execute.md`). | None: the session and its branch are the claim. |
| Inbox | Comments on the task's issue. | The live session, and the PR's comments. |
| `med` and `high` rulings, deviations, outcome | Comments on the task's issue, repeated in the PR body. | The PR body only. |
| Ruling review | The `engine:ruling-review` label. | Not used. |
| Close on merge | `Closes #<task>` in the PR body. | The PR deletes the card and marks it completed in the milestone. The last card's PR deletes the milestone. |

## Card fields

| Field | Meaning | GitHub | In-repo plan (default) |
| --- | --- | --- | --- |
| Outcome | What the PR makes true, provable by a test. | `## Outcome` in the body | `## Outcome` in the card |
| Decided | Fixed by Prepare or earlier tasks, linked to the owning doc. | `## Decided` | `## Decided` |
| Open | Questions the executor decides, graded per `rulings.md`. | `## Open` | `## Open` |
| Tests | The claims to prove, and the layer each belongs in. | `## Tests` | `## Tests` |
| Docs to update | The owning docs the change touches. | `## Docs to update` | `## Docs to update` |
| Gates | Lanes to run beyond the repo's default. | `## Gates` | `## Gates` |
| Overrides | Repo rules waived for this task, each as `<rule> → <instead>`. Adds to the feature's. | `## Overrides`, on the card and on the feature | Not used: the operator is in the session. |
| Requires | Host capabilities, such as `ios`, `docker` or `android`. | `## Requires` | Not used: the operator picks the host. |
| Harness / tier | Optional. Empty means the default harness and `standard`. | `## Harness / tier` | Not used: the operator picks the model. |
| Depends on | Tasks that must merge first. | Native "blocked by" links only | The milestone task table's Depends on column |
| Parent | The feature. | Native sub-issue link | The milestone's task table lists the card |
| Card key | Stable identity, so a rerun planner skips existing cards. | `<!-- engine:card <key> -->` in the body | The card's file name |

Heading names are exact: the coordinator and plan lint find fields by them.
