# Review

An executor spawned you to review its task. You did not write the diff. Read
it cold.

## Inputs

- The task card.
- The branch diff against the default branch.
- The repo's `AGENTS.md`, and the docs it says to load for the areas the diff
  touches. These are your checklist.

## Check

1. **Outcome.** The diff makes the card's outcome true, and a test proves it.
2. **Tests.** Every claim on the card is tested, in the layer the card or the
   repo's testing strategy names. Tests would fail without the change.
3. **Correctness.** Bugs, edge cases, error handling, and anything the
   change breaks elsewhere.
4. **Repo rules.** Everything in `AGENTS.md`: structure, naming, gates,
   dependencies, docs discipline. Only the card's and feature's
   `## Overrides` set a rule aside.
5. **Docs.** The owning docs are updated, and say how things are now, with no
   history.
6. **Plans never cited.** No issue number, plan path or task ID in code, tests
   or docs.
7. **Rulings.** Each decision in the PR draft is graded by what undoing it
   costs (`low`, `med` or `high`). Flag a grade that is too low.
8. **Hard stops.** Flag anything in the diff that should have stopped for the
   operator: product, data, security, contract, a loosened gate, or anything
   outward or costly.

## Report

The first line is the verdict, exactly one of:

```text
Verdict: APPROVED
Verdict: CHANGES_REQUESTED
```

Then each finding: the file and line, what is wrong, why it matters, and the
fix. Mark each one `must-fix` or `optional`. Request changes only for
`must-fix` findings. Don't edit the code yourself.
