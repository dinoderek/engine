# Hard stops

A hard stop is the only time you ask the operator and wait. Everything else
you decide (`SKILL.md`, "Decisions").

## Classes

1. **Product:** a product decision that neither the docs nor the requirements
   answer.
2. **Data:** a destructive or irreversible data change, such as a migration
   that drops or rewrites data, or a backfill.
3. **Security:** auth, permissions, row-level security, or secrets handling.
4. **Contract:** a change the design doesn't cover to a public API, schema,
   protocol or CLI surface that others use.
5. **Gates:** loosening a gate (a threshold, a suppression or a baseline
   entry), or a required gate that is red on the default branch itself.
6. **Outward or costly:** deploying, touching hosted environments, store
   releases, paid services, or sending messages.
7. **Drift:** the default branch has moved in a way that invalidates the card.
8. **Objectives:** the requirements would have to change.
9. **Review:** a finding you and the reviewer still disagree on after a
   round, or one that asks for a change against the card or its design.
10. **Environment:** a required gate, tool, sign-in or setting this host
    lacks.
11. **Repo rule:** a repo rule tells you to involve the operator, and no
    override on the card covers it.
12. **Permission:** an unattended session's permission request was denied,
    and no route without a new permission works (for example `cat` in place
    of a denied Read).

The repo's `AGENTS.md` may add classes; they apply too.

## Ask once

Before asking, look for an answer the operator has already given (in the
session, on the task, or, in an engine task, on its plan). One that decides
the **same root cause** covers you: the same failing gate on the default
branch, the same broken tool or sign-in on the same host, the same product
question. Follow it, cite where it was given in the PR body, and carry on.
If a question on the same root cause is already open, wait on it instead of
asking again, and cite it and its answer in the PR body.

Never stretch an answer to a different question: a ruling on one red lane
does not cover another lane, and a host's sign-in fix does not cover another
host. If unsure, it does not cover you.

## Asking

Give the class, the question, your recommended answer, and the evidence.

- **A gate red on the default branch:** prove it first. Run the failing lane
  in a scratch checkout of the default branch's current head (`git worktree
  add --detach <scratch> origin/<default>`, removed afterwards). Then ask
  about the default branch, not your diff, quoting the commit and the
  failing output, so one answer serves every task that hits it.
- **A `gh` sign-in, or another host tool, is broken:** name the host (its
  logical name in `~/.config/engine/host`) and the failing output, such as
  `gh auth status`, and point the operator at `engine setup --host <name>`,
  run from their machine: it checks the host and prints the fix. Never run
  setup yourself.
- **Permission:** the tool, its exact input, why you need it, and the
  permission rule that would allow it. The operator adds the rule in the
  session and tells you to retry.

Then end your turn and wait. Reading, acknowledgement, a conversational reply
or an automated `continue` grant nothing: only the operator's answer to the
question does. When it comes, follow it and carry on. An engine server task
raises the question with `engine ask` instead, on its plan when the root
cause affects every task (the `engine` skill).
