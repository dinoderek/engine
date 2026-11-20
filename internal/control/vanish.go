package control

import "time"

// ClaimWait is how long a started session has to claim its task: write to
// the server, or stay live. A window that dies sooner without either ended
// before it did any work.
const ClaimWait = 5 * time.Minute

// The reasons an attempt ends: a confirmed-dead window, or the operator.
const (
	// EndVanished leaves the task resumable: its next dispatch is a resume.
	EndVanished = "vanished"
	// EndEarlyDeath is a first death before the session claimed its task: the
	// task goes back to todo, for the next pass to dispatch a new attempt. A
	// second early death of the task ends failed, with Early set.
	EndEarlyDeath = "early-death"
	// EndFailed marks the task failed; only the operator moves it to todo.
	EndFailed = "failed"
	// EndStopped is the operator's stop: the task is todo and held, and its
	// worktree and branch are kept for the resume.
	EndStopped = "stopped"
)

// resumable reports whether task item still has work to resume after its
// attempt at's window was confirmed gone: its coordination was stopped after
// the window was last seen live, as when a host reboots while the operator
// has a plan stopped; or the window was seen live, the task has a PR or an
// open operator question, and the attempt was not itself the resume of a
// vanished one. So a session that crashes on launch, or every resume of
// one, is not dispatched again and again.
func resumable(st *State, item Item, at Attempt) bool {
	work := item.PR != "" || OwnQuestion(st, item.ID) > 0
	if work && !at.LiveAt.IsZero() && !resumedVanished(st, at) {
		return true
	}
	stopped := Root(st, item).StoppedAt
	last := at.Started
	if at.LiveAt.After(last) {
		last = at.LiveAt
	}
	return !stopped.IsZero() && !stopped.Before(last)
}

// resumedVanished reports whether the attempt of at's task that ran just
// before at ended vanished. Attempt numbers are allocated in order. An
// attempt that never opened a window, such as a dispatch the operator ended
// unlaunched, ran nothing, so at is not its resume.
func resumedVanished(st *State, at Attempt) bool {
	var prev Attempt
	for _, o := range st.Attempts {
		if o.Item == at.Item && o.Window != "" && Number(o.ID) < Number(at.ID) && Number(o.ID) > Number(prev.ID) {
			prev = o
		}
	}
	return prev.Reason == EndVanished
}

// Vanished reports whether item is an unfinished task whose current attempt
// ended vanished or in an early death, so coordination dispatches it again as a resume.
func Vanished(st *State, item Item) bool {
	at, ok := st.Attempts[item.Attempt]
	return ok && item.Kind == "task" && at.State == "ended" && (at.Reason == EndVanished || at.Reason == EndEarlyDeath) &&
		item.State != "done" && item.State != "failed"
}

// claimed reports whether at's session claimed its task: it wrote to the
// server, or its window was seen live for ClaimWait.
func claimed(st *State, at Attempt) bool {
	if !at.LiveAt.IsZero() && at.LiveAt.Sub(at.Started) >= ClaimWait {
		return true
	}
	for _, u := range st.Updates {
		if u.Author == at.ID {
			return true
		}
	}
	return false
}

// earlyDeaths counts the earlier attempts of at's task that died before
// claiming it.
func earlyDeaths(st *State, at Attempt) int {
	n := 0
	for _, o := range st.Attempts {
		if o.Item == at.Item && o.ID != at.ID && o.Early {
			n++
		}
	}
	return n
}

// endDead ends at, whose window a host read confirmed gone. An unfinished
// task's current attempt ends vanished when the task is resumable, leaving
// its state alone, and its merge queue entry stays queued. A window that died
// before its session claimed the task ends early-death the first time, with
// a running task back in todo, and failed the second. Otherwise it ends
// failed and so does the task, which leaves the queue.
func (s Service) endDead(st *State, a Actor, at Attempt, body string) Attempt {
	item := st.Items[at.Item]
	at.State, at.Ended = "ended", s.now()
	if item.Kind == "task" && item.Attempt == at.ID && item.State != "done" && item.State != "failed" {
		at.Reason = EndFailed
		resume := resumable(st, item, at)
		early := !resume && !claimed(st, at)
		switch {
		case resume:
			at.Reason = EndVanished
		case early && earlyDeaths(st, at) == 0:
			at.Reason = EndEarlyDeath
			if item.State == "running" {
				item.State = "todo"
				item.Version++
				st.Items[item.ID] = item
			}
		default:
			withdraw(st, a, item, "its session failed", s.now())
			item.State = "failed"
			item.Version++
			st.Items[item.ID] = item
		}
		at.Early = early
	}
	if !at.Early {
		at.Tail = ""
	}
	st.Attempts[at.ID] = at
	if at.Reason != "" {
		body = at.Reason + ": " + body
	}
	st.update(a, Request{ID: at.Item}, "execution-ended", body, s.now())
	return at
}

// endUnlaunched ends a dispatching attempt that its host confirmed never
// launched: no window and no run record. A launch that never happened is no
// failure of the task, so the attempt ends vanished and a running task
// returns to todo, for the next pass to dispatch a new attempt.
func (s Service) endUnlaunched(st *State, a Actor, at Attempt, body string) Attempt {
	item := st.Items[at.Item]
	at.State, at.Ended, at.Reason = "ended", s.now(), EndVanished
	st.Attempts[at.ID] = at
	if item.Kind == "task" && item.Attempt == at.ID && item.State == "running" {
		item.State = "todo"
		item.Version++
		st.Items[item.ID] = item
	}
	st.update(a, Request{ID: at.Item}, "execution-ended", at.Reason+": "+body, s.now())
	return at
}

// EndDead ends attempt id after the coordinator saw its window gone on a
// host it read, with the tail of its pane log. The tail is kept only when
// the session died before claiming its task. It is idempotent and returns
// the ended attempt.
func (s Service) EndDead(id, body, tail string) (Attempt, error) {
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
		at.Tail = tail
		out = s.endDead(st, Actor{Name: "coordinator", Role: "coordinator"}, at, body)
		return nil
	})
	return out, err
}
