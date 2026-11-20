package control

// stopTask is the operator's stop of a task's execution, in one
// transaction: the task is held, its attempt ends EndStopped, and the task
// returns to todo, leaving the merge queue. The hold is set before the
// attempt ends, so no pass can place the task in between. The coordinator
// then closes the window, keeping the worktree and branch. A task with no
// execution to end, such as a todo or finished one, is refused.
func (s Service) stopTask(st *State, a Actor, r Request) (Response, error) {
	item, err := heldTarget(st, a, r)
	if err != nil {
		return Response{}, err
	}
	at, ok := st.Attempts[item.Attempt]
	if !ok || at.State == "ended" || item.State == "done" || item.State == "failed" {
		return Response{}, problem(409, "task %s has no running execution to stop", item.ID)
	}
	withdraw(st, a, item, "the operator stopped the task", s.now())
	at.State, at.Ended, at.Reason = "ended", s.now(), EndStopped
	st.Attempts[at.ID] = at
	item.State, item.Held = "todo", true
	item.Version++
	st.Items[item.ID] = item
	st.update(a, r, "execution-ended", EndStopped+": the operator stopped the task; it is held until resumed", s.now())
	return Response{Item: &item, Attempt: &at}, nil
}

// resumeTask lifts a task's hold. Its next dispatch resumes the branch its
// earlier attempt left.
func (s Service) resumeTask(st *State, a Actor, r Request) (Response, error) {
	item, err := heldTarget(st, a, r)
	if err != nil {
		return Response{}, err
	}
	if !item.Held {
		return Response{}, problem(409, "task %s is not held", item.ID)
	}
	item.Held = false
	item.Version++
	st.Items[item.ID] = item
	st.update(a, r, "resumed", "the hold was lifted; the task is dispatched again as a resume", s.now())
	return Response{Item: &item}, nil
}

// heldTarget is the task an operator's stop or resume names, checked. A
// version, when given, must be the task's current one.
func heldTarget(st *State, a Actor, r Request) (Item, error) {
	if err := operator(a); err != nil {
		return Item{}, err
	}
	item, ok := st.Items[r.ID]
	if !ok || item.Kind != "task" {
		return Item{}, problem(404, "unknown task")
	}
	if r.Version != 0 && r.Version != item.Version {
		return Item{}, problem(409, "version is %d", item.Version)
	}
	return item, nil
}
