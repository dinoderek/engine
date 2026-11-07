package control

import "testing"

func TestWorkerReadScopeIncludesDependenciesAndExcludesOtherInboxes(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	upstream := newTask(t, s, repo.ID, plan.ID)
	own := newTask(t, s, repo.ID, plan.ID)
	other := newTask(t, s, repo.ID, plan.ID)
	own = *call(t, s, op, Request{Op: "edit", ID: own.ID, Version: own.Version, Item: Item{Title: "Dependent", Depends: []string{upstream.ID}}}).Item
	approve(t, s, plan)
	ua, up := dispatch(t, s, upstream)
	call(t, s, ua, Request{Op: "state", ID: upstream.ID, Version: up.Item.Version, State: "done"})
	a, _ := dispatch(t, s, own)
	call(t, s, op, Request{Op: "say", ID: other.ID, Body: "Other task's message", Recipient: "operator"})
	call(t, s, op, Request{Op: "say", Body: "Private system message", Recipient: "operator"})
	mine := call(t, s, a, Request{Op: "say", Body: "My status", Recipient: "operator"}).Updates[0]
	call(t, s, op, Request{Op: "reply", ReplyTo: mine.ID, Body: "Acknowledged by operator"})
	reject(t, s, a, Request{Op: "get", ID: other.ID}, 403)
	reject(t, s, a, Request{Op: "inbox"}, 403)
	for _, id := range []string{own.ID, upstream.ID, plan.ID, repo.ID} {
		call(t, s, a, Request{Op: "get", ID: id})
	}
	for _, u := range call(t, s, a, Request{Op: "updates"}).Updates {
		if u.Body == "Private system message" || u.Body == "Other task's message" {
			t.Fatal("worker read another inbox")
		}
	}
	for _, i := range call(t, s, a, Request{Op: "list"}).Items {
		if i.ID == other.ID {
			t.Fatal("worker listed sibling outside scope")
		}
	}
}

func TestStoppedCoordinationCannotReserveNewAttempt(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	task := newTask(t, s, repo.ID, plan.ID)
	approve(t, s, plan)
	call(t, s, op, Request{Op: "run", ID: plan.ID})
	call(t, s, op, Request{Op: "stop", ID: plan.ID})
	if _, err := s.Dispatch(task.ID, "devbox", "claude", "stale-action"); err == nil {
		t.Fatal("stopped plan dispatched")
	}
	st, _ := s.Store.Snapshot()
	if len(st.Attempts) != 0 {
		t.Fatal("stopped plan reserved execution")
	}
	reject(t, s, op, Request{Op: "state", ID: task.ID, Version: task.Version, State: "running"}, 409)
}
