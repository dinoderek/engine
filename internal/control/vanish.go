package control

// The reasons a confirmed-dead window ends an attempt.
const (
	// EndVanished leaves the task resumable: its next dispatch is a resume.
	EndVanished = "vanished"
	// EndFailed marks the task failed; only the operator moves it to todo.
	EndFailed = "failed"
)

// resumable reports whether task item still has work to resume after its
// attempt at's window was confirmed gone: its coordination was stopped after
// the window was last seen live, as when a host reboots while the operator
// has a plan stopped; or the window was seen live and the task has a PR or
// an open operator question. A session that dies before it is ever seen live
// fails, so one that crashes on launch is not dispatched again and again.
func resumable(st *State, item Item, at Attempt) bool {
	if !at.LiveAt.IsZero() && (item.PR != "" || PendingQuestion(st, item.ID) > 0) {
		return true
	}
	stopped := item.StoppedAt
	if plan, ok := st.Items[item.Plan]; ok && plan.StoppedAt.After(stopped) {
		stopped = plan.StoppedAt
	}
	last := at.Started
	if at.LiveAt.After(last) {
		last = at.LiveAt
	}
	return !stopped.IsZero() && !stopped.Before(last)
}

// Vanished reports whether item is an unfinished task whose current attempt
// ended vanished, so coordination dispatches it again as a resume.
func Vanished(st *State, item Item) bool {
	at, ok := st.Attempts[item.Attempt]
	return ok && item.Kind == "task" && at.State == "ended" && at.Reason == EndVanished &&
		item.State != "done" && item.State != "failed"
}

// endDead ends at, whose window a host read confirmed gone. An unfinished
// task's current attempt ends vanished when the task is resumable, leaving
// its state alone; otherwise it ends failed and so does the task. A merge
// lock the attempt holds stays with it for operator recovery.
func (s Service) endDead(st *State, a Actor, at Attempt, body string) Attempt {
	item := st.Items[at.Item]
	at.State, at.Ended = "ended", s.now()
	if item.Kind == "task" && item.Attempt == at.ID && item.State != "done" && item.State != "failed" {
		at.Reason = EndFailed
		if resumable(st, item, at) {
			at.Reason = EndVanished
		} else {
			item.State = "failed"
			item.Version++
			st.Items[item.ID] = item
		}
	}
	st.Attempts[at.ID] = at
	if at.Reason != "" {
		body = at.Reason + ": " + body
	}
	st.update(a, Request{ID: at.Item}, "execution-ended", body, s.now())
	return at
}

// EndDead ends attempt id after the coordinator saw its window gone on a
// host it read. It is idempotent and returns the ended attempt.
func (s Service) EndDead(id, body string) (Attempt, error) {
	var out Attempt
	err := s.Store.Transaction(func(st *State) error {
		at, ok := st.Attempts[id]
		if !ok {
			return problem(404, "unknown execution")
		}
		if at.State == "ended" {
			out = at
			return nil
		}
		out = s.endDead(st, Actor{Name: "coordinator", Role: "coordinator"}, at, body)
		return nil
	})
	return out, err
}
