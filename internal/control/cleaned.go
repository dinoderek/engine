package control

// Cleaned is the update kind of an executor's report that it has torn down
// what it provisioned for its merged task. The task stays done; the report
// lets the coordinator release the session at once.
const Cleaned = "cleaned"

// cleaned records the cleanup report of a worker's own done task. Left, the
// request's body, names anything it could not remove. A repeated report is
// recorded too; the newest one counts.
func (s Service) cleaned(tx *Tx, a Actor, r Request) (Response, error) {
	if a.Role != "worker" {
		return Response{}, problem(403, "only the task's execution can report its cleanup")
	}
	item, ok := tx.Item(r.ID)
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
	u := tx.AppendUpdate(a, Request{ID: item.ID}, Cleaned, body, s.now())
	u.Left = r.Body
	tx.PutUpdate(u)
	return Response{Updates: []Update{u}}, nil
}

// CleanedUp is the task's newest cleanup report, if it has one.
func CleanedUp(tx *Tx, id string) (Update, bool) {
	return tx.LatestUpdate(id, Cleaned)
}
