# Rulings and hard stops

You decide open questions yourself. Log as rulings only the decisions the
operator might want to reverse, and stop only for a hard stop.

## What you own

Decide these without asking:

- internal design, naming and structure;
- which test layer a claim belongs in;
- choices the design left open, including the card's **Open** questions;
- refactors inside the modules the task touches;
- splitting your task, or adding `todo` tasks inside the feature's scope;
- UI details within the agreed mocks;
- adding dev-only dependencies.

## Grading a decision

Anything a code review of the diff would catch (naming, internal structure,
test layer, a refactor inside the touched modules) is not a ruling. Don't log
it.

Grade everything else by what undoing it would cost, not by how hard it was to
decide:

| Risk | When | Where it goes |
| --- | --- | --- |
| `low` | Undoing it touches only this PR's files. Nothing user-visible, persisted, or built on by another task. | One line in the PR body, under a collapsed `<details><summary>Low-risk decisions</summary>`. Nowhere else. |
| `med` | Undoing it touches other modules, changes user-visible behaviour within the mocks, or later tasks build on it. | A ruling comment on the task (see `plan-sources.md`), repeated in the PR body. Add `engine:ruling-review` to the task issue. |
| `high` | Hard to undo once merged: a shape other code or other tasks depend on, a persisted format, or a convention across features. | As `med`, and @mention the operator in the comment. Then carry on. |

When unsure between two grades, pick the higher one.

The ruling comment:

```text
Ruling (risk: med|high): <decision>
Why: <reason>
If wrong: <what it costs to undo, what it touches>
```

## Hard stops

For any of these, stop: set the task to `needs-human`, @mention the operator
with the question and your recommended answer, and wait in the live session.
With an in-repo plan, ask the operator in the session.

1. **Product:** a product decision that neither the docs nor the requirements
   answer.
2. **Data:** a destructive or irreversible data change, such as a migration
   that drops or rewrites data, or a backfill.
3. **Security:** auth, permissions, row-level security, or secrets handling.
4. **Contract:** a change the design doesn't cover to a public API, schema,
   protocol or CLI surface that others use.
5. **Gates:** loosening a gate (a threshold, a suppression or a baseline entry).
6. **Outward or costly:** deploying, touching hosted environments, store
   releases, paid services, or sending messages.
7. **Drift:** the default branch has moved in a way that invalidates the card.
8. **Objectives:** the feature's requirements would have to change.
9. **Review:** two review rounds that don't converge.
10. **Environment:** a required gate that cannot run on this host.
11. **Repo rule:** a repo rule tells you to involve the operator, and no
    override on the card or its feature covers it.

The repo's `AGENTS.md` may add classes, and they apply too. When the operator
answers, read the answer (in the session or on the task), remove
`needs-human`, and carry on.
