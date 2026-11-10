package control

// Cleaned is the update kind of an executor's report that it has torn down
// what it provisioned for its merged task. The task stays done; the report
// lets the coordinator release the session at once.
const Cleaned = "cleaned"

// cleaned records the cleanup report of a worker's own done task. Left, the
// request's body, names anything it could not remove. A repeated report is
// recorded too; the newest one counts.
func (s Service) cleaned(st *State, a Actor, r Request) (Response, error) {
	if a.Role != "worker" {
		return Response{}, problem(403, "only the task's execution can report its cleanup")
	}
	item, ok := st.Items[r.ID]
	if !ok || item.Kind != "task" || item.Attempt != a.Attempt {
		return Response{}, problem(403, "only the task's execution can report its cleanup")
	}
	if item.State != "done" {
		return Response{}, problem(409, "a cleanup report needs the task done, not %s", item.State)
	}
	body := "Cleaned up after the merge."
	if r.Body != "" {
		body = "Cleaned up after the merge, but left: " + r.Body
	}
	u := st.update(a, Request{ID: item.ID}, Cleaned, body, s.now())
	u.Left = r.Body
	st.Updates[len(st.Updates)-1] = u
	return Response{Updates: []Update{u}}, nil
}

// CleanedUp is the task's newest cleanup report, if it has one.
func CleanedUp(st *State, id string) (Update, bool) {
	for i := len(st.Updates) - 1; i >= 0; i-- {
		if u := st.Updates[i]; u.Item == id && u.Kind == Cleaned {
			return u, true
		}
	}
	return Update{}, false
}
