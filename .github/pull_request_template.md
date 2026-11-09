<!-- Data and file pointers, not prose. Link, don't quote. -->

## Objective

<!-- What this PR makes true, in 1–3 lines. For a task issue: Closes #<task>. -->

## Gates

| Lane | Result | Evidence |
| --- | --- | --- |
| `./dev test` (every required lane) | PASS / FAIL | `.test-ai-results/summary.md` excerpt or CI run link |

- New or changed tests: <!-- file paths, one line each: the behaviour each proves -->
- Not run, with reason: <!-- a lane CI cannot run, with its local evidence; or "none" -->

## Rulings

<!-- At most five `low` notes, each one line:
Ruling (risk: low): <decision>
and any `review` rulings, each as:
Ruling (risk: review): <decision>
User notices: <what a user would see, or nothing>
Alternative: <the option not taken>
If wrong: <what it costs to undo, what it touches>
Cite any operator answer this task followed. "None" if none. -->

## Review

- Fresh reviewer: <!-- harness and model; verdict -->
- Declined findings: <!-- each with the reason, or "none" -->
- Review hard: <!-- the 1–3 places to scrutinise, as file paths, and why -->

## Deviations

<!-- Anything that departs from AGENTS.md or the docs: new baseline rows or
//nolint directives, raised budgets, lowered thresholds (each with the
operator's agreement), skipped lanes. "None" if none. -->
