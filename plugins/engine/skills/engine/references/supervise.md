# Supervise a server plan

The coordinator inside `engine serve` runs the plan; you watch it and fill
only the gaps it cannot. Kickoff: `supervise <plan>`. Every gap you fill is a
coordinator defect, so you record each one.

## Watch

The operator has approved the plan and run `engine run <plan>`, which records
the intent; the server coordinates. Check every 10 minutes:

- `engine ps`: each task's state and attempt; `engine attempt list <task>`
  for its host and window;
- `engine inbox --operator`: open questions, notices and rulings digests;
- `engine updates <plan>` and `engine updates <task>` for each task, from
  your read cursor;
- `engine merge-queue <repo>`: the order merges wait in.

`engine ui` is the operator's own view. The plan ends at `done`.

## You may

- **Relay an answer** the operator gave you: `engine tell <task> <text>`, or
  once on the plan for several of its tasks, which wakes each waiting one.
  Answer an inbox question with `engine reply <sequence> <text>`. Leave
  `engine resolve` and every state change, the operator's decisions, to the
  operator unless they gave them to you.
- **Answer a stall** the coordinator raised, with `engine tell`, when the
  answer is not a hard stop: for example "the plan ruling covers your question, carry on".
- **Count rulings.** A task whose digest holds more than two `review`
  rulings had a weak card; note it for the next Prepare.

## You never

Change a state the operator did not give you, approve, start, stop, claim
or kill anything; restart
`engine serve`; edit the target repo; merge; or answer a hard stop. Those
belong to the coordinator, the executors and the operator.

## Recording

- **Each intervention:** `engine tell <plan> "Supervisor: <what you did>.
  Missed: <what the coordinator should have done>."`
- **An engine bug:** an issue on `dinoderek/engine` with the evidence (the
  updates, the state it saw, what it did). Cite it in your plan message.
- **At the end:** when the plan is `done`, post one plan message listing
  every intervention and the tasks with more than two `review` rulings, then
  end the session.
