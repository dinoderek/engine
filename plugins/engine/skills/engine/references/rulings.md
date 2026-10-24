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
| `med` | Undoing it touches other modules, changes user-visible behaviour within the mocks, or later tasks build on it. | A ruling comment on the task (see `plan-sources.md`), repeated in the PR body. |
| `high` | Hard to undo once merged: a shape other code or other tasks depend on, a persisted format, or a convention across features. | As `med`, plus `engine:ruling-review` on the task issue and an @mention of the operator in the comment. Then carry on. |

When unsure between two grades, pick the higher one.

The ruling comment:

```text
Ruling (risk: med|high): <decision>
User notices: <what a user of the product would see, in product terms>
Alternative: <the option you did not take>
Why: <reason>
If wrong: <what it costs to undo, what it touches>
```

Write it for someone who has not read the code: name screens, settings and
behaviour, not types or functions. `User notices: nothing` is a valid answer.

In gated mode, a ruling the operator made ends with `Decided by: operator`.
It is logged at the grade its undo cost earns, but adds no
`engine:ruling-review` and no @mention, because the operator has already seen
it.

## Hard stops

For any of these, stop. Swap the task's state label (`engine:running` or
`engine:in-review`) for `engine:needs-human`, and comment with an @mention of
the operator, giving the question and your recommended answer. End your turn
and wait in the live session. With an in-repo plan, ask the operator in the
session.

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
10. **Environment:** a required gate, tool or setting this host lacks.
11. **Repo rule:** a repo rule tells you to involve the operator, and no
    override on the card or its feature covers it.
12. **Permission:** an unattended session's permission request was denied,
    and no route without a new permission works (for example `cat` in place of
    a denied Read). Comment with the tool, its exact input, why you need it,
    and the permission rule that would allow it. The operator adds the rule
    in the session and tells you to retry.

The repo's `AGENTS.md` may add classes, and they apply too. When the operator
answers, read the answer (in the session or on the task), restore the label
that `needs-human` replaced, and carry on.
