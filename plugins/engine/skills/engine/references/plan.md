# Plan

You turn the operator's ask into a server plan, or one standalone task,
that executors can run hands-off, and break an approved plan into tasks.
You run at the `deep` tier.

| Mode | Who is with you | You produce |
| --- | --- | --- |
| `plan <ask>` | The operator | A plan (or standalone task) in `prepare`, holding the agreed requirements and design (or card). |
| `server planner <plan>` | Nobody: the coordinator started you | The plan's cards, a passing `engine lint`, and the plan `planned`. |

After Prepare the operator approves (`engine plan approve <plan> --version
<n>`, or `engine task approve`) and runs `engine run <id>`. The coordinator
then starts a planner on a plan, and dispatches each ready task once the plan
is `planned`.

## Prepare: batches

Work in batches of increasing detail. Never ask one question at a time.

1. Read the code and the docs the ask touches. Draft the **whole** next
   layer at once: L1 requirements (objectives, scope, surfaces), then L2
   design (approach, risks and answers, API, mocks, what is left to the
   agent). Recommend an answer for everything you can decide; list only the
   questions that need the operator. A standalone task has one layer: its
   card.
2. Put the draft in a **Claude Doc**, so the operator can comment on the
   text. On the first batch, also create the server item, its body only a
   link to the doc: `engine plan create --repo <repo> --title <text>
   --body-file <path>`, or `engine task create` without `--plan` for a
   standalone task. `engine repo list` gives the repository's ID. Each later
   batch updates the doc in place.
3. The draft ends with a numbered `## Questions` section. Tell the operator
   in chat which questions matter most.
4. The operator answers in one batch: comments on the doc, or chat. Reply in
   each comment thread with what you changed. Fold the answers in, and go one
   layer deeper.

Stop when `## Questions` is empty. Then write the final text into the body
(`engine plan edit <plan> --version <n> --title <text> --body-file <path>`,
or `engine task edit`), keep the doc link at the bottom, and tell the
operator it is ready to approve. An edit replaces every field: resend any
harness, tier or requires. Executors read only the body.

Without the Claude Docs connector (for example in Codex), draft in the body
itself: edit it on each batch and tell the operator what changed.

## Plan body

| Heading | Content |
| --- | --- |
| `## Objectives` | `O1`, `O2`, …: each one provable by a test. The acceptance task cites them. |
| `## Scope` | In, and out. |
| `## Surfaces` | The APIs, schemas, CLIs and screens it touches. |
| `## Design` | The approach, and the risks with their answers. Link the mocks. |
| `## Left to the agent` | The questions executors decide themselves. |
| `## Overrides` | Repo rules this plan waives, each as `<rule> → <instead>`. |
| `## Questions` | For the operator. Empty when ready to approve. |

Answer every open question here or move it to `## Left to the agent`. Prepare
is the only time the operator is consulted, so nothing may stay vague.

## Repo rules that block hands-off

In the first batch, read the repo's gates and workflow rules (`AGENTS.md`,
the lane registry and its docs, the PR template, any review procedure) and
sort every rule that tells an agent to involve the operator:

- **Compatible** (run a named lane on every change): executors follow it.
- **Blocks hands-off** (agree lanes with the operator; never merge unless
  asked): a question with two options, the operator changes the rule in the
  repo (a separate PR) or this plan overrides it. Recommend one.
- **A real stop** (an open product fact needs its owner): it stays a hard
  stop; if the work needs the answer, ask it now.

Check that every lane the work needs runs on some host, and name the
capability in the design for the cards' `--requires`; a lane no host can run
is a question. Never
invent an override the operator has not agreed.

## Budget headroom

For each doc the work will update, put its word budget (the repo's budget
registry) and current count (its doc check, else `wc -w`) in the design. A doc the work would push over budget is a question: a trim task
first, or a raise the operator agrees. Recommend the trim.

## Card

A card is a task's body plus fields set by flags. Headings are exact: lint
finds fields by them.

| Field | Where | Meaning |
| --- | --- | --- |
| Outcome | `## Outcome` | What the PR makes true, provable by a test. Required. |
| Decided | `## Decided` | Fixed by Prepare or earlier tasks; link the owning doc. |
| Open | `## Open` | A list of questions the executor decides without the operator. |
| Tests | `## Tests` | The claims to prove, and the layer each belongs in. Required. |
| Docs to update | `## Docs to update` | The owning docs the change touches. |
| Gates | `## Gates` | Lanes beyond the repo's default, for the paths touched. |
| Overrides | `## Overrides` | Only those this card adds to its plan's. |
| Depends on | `--depends <id>,<id>` | Tasks that must be `done` first. |
| Requires | `--requires <cap>,<cap>` | Host capabilities, such as `docker`. |
| Harness, tier | `--harness`, `--tier` | Optional. Empty means the default harness and `standard`. |

- **Open:** a product question belongs in the plan's `## Questions`, not
  here. Each open question has one owner card. Another card that depends on
  the answer lists it under **Decided** as `<question>: per <owner card>`,
  and depends on the owner.
- **Size:** one PR each. About 2000 added lines is a guide, not a limit.
- **Tier:** `deep` for the acceptance task and for cards whose Open
  questions may lead to `review` rulings, that sit next to a hard-stop class,
  or that span several modules; `light` for docs-only or mechanical cards;
  otherwise leave it empty (`standard`).

## Breakdown

1. Read the plan, its updates, the repo's `AGENTS.md`, and the code it
   touches. Reuse any card `engine task list <plan>` already shows.
2. Draft every card. Add a final acceptance task that proves each objective
   end to end and depends on every other task.
3. **Write in two passes** (`server.md`, "Planner"): create every card,
   then add dependencies and owner card IDs.
4. **Lint** with `engine lint <plan>` (cycles, outcome and tests, shared
   open questions, the acceptance task) until it passes.
5. A rule that blocks hands-off and that no override covers, or a budget
   that would overflow, is a hard stop: ask (`server.md`, "Planner").
6. Set the plan `planned` and end the session (`server.md`, "Planner").
