package control

import (
	"strings"
	"testing"
)

var coordinator = Actor{Name: "coordinator", Role: "coordinator"}

const head = "0123456789abcdef0123456789abcdef01234567"

// inReview dispatches a task, moves it to in-review and takes the merge lock.
func inReview(t *testing.T, s Service) (Item, Actor, Response, *Lock) {
	t.Helper()
	repo, plan := repoPlan(t, s)
	task := newTask(t, s, repo.ID, plan.ID)
	approve(t, s, plan)
	a, claimed := dispatch(t, s, task)
	call(t, s, a, Request{Op: "state", ID: task.ID, Version: claimed.Item.Version, State: "in-review", Item: Item{PR: good().PR}})
	lock := call(t, s, a, Request{Op: "lock-acquire", Attempt: claimed.Attempt.ID}).Lock
	return task, a, claimed, lock
}

func ready(id, token string, m MergeRequest) Request {
	return Request{Op: "merge-request", ID: id, Token: token, Merge: &m}
}

func good() MergeRequest {
	return MergeRequest{PR: externalPR("owner/demo"), Head: head, Method: "squash", Verdict: "APPROVED"}
}

func TestMergeRequestValidation(t *testing.T) {
	s := service(t)
	task, a, _, lock := inReview(t, s)
	bad := func(edit func(*MergeRequest)) MergeRequest { m := good(); edit(&m); return m }
	for name, c := range map[string]struct {
		r      Request
		status int
	}{
		"no details":      {Request{Op: "merge-request", ID: task.ID, Token: lock.Token}, 400},
		"other repo PR":   {ready(task.ID, lock.Token, bad(func(m *MergeRequest) { m.PR = externalPR("x/y") })), 400},
		"short head":      {ready(task.ID, lock.Token, bad(func(m *MergeRequest) { m.Head = "0123abc" })), 400},
		"unknown method":  {ready(task.ID, lock.Token, bad(func(m *MergeRequest) { m.Method = "admin" })), 400},
		"not approved":    {ready(task.ID, lock.Token, bad(func(m *MergeRequest) { m.Verdict = "CHANGES_REQUESTED" })), 409},
		"wrong lock":      {ready(task.ID, "stale", good()), 409},
		"another PR":      {ready(task.ID, lock.Token, bad(func(m *MergeRequest) { m.PR = externalPR("owner/demo") + "3" })), 409},
		"another task":    {ready("t99", lock.Token, good()), 403},
		"operator cannot": {ready(task.ID, lock.Token, good()), 403},
	} {
		actor := a
		if name == "operator cannot" {
			actor = op
		}
		t.Run(name, func(t *testing.T) { reject(t, s, actor, c.r, c.status) })
	}
	st := mustSnapshot(t, s)
	if _, ok := PendingMerge(&st, task.ID); ok {
		t.Fatal("a refused request was recorded")
	}
}

func TestMergeRequestNeedsInReviewTheLockAndNoQuestion(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	task := newTask(t, s, repo.ID, plan.ID)
	approve(t, s, plan)
	a, claimed := dispatch(t, s, task)
	reject(t, s, a, ready(task.ID, "", good()), 409) // running, no lock
	call(t, s, a, Request{Op: "state", ID: task.ID, Version: claimed.Item.Version, State: "in-review", Item: Item{PR: good().PR}})
	reject(t, s, a, ready(task.ID, "", good()), 409) // no lock
	lock := call(t, s, a, Request{Op: "lock-acquire", Attempt: claimed.Attempt.ID}).Lock
	question := call(t, s, a, Request{Op: "ask", ID: task.ID, Body: "Approve the merge?"}).Updates[0]
	reject(t, s, a, ready(task.ID, lock.Token, good()), 409)
	call(t, s, op, Request{Op: "resolve", ReplyTo: question.ID, Body: "Approved"})
	u := call(t, s, a, ready(task.ID, lock.Token, good())).Updates[0]
	if u.Kind != "merge-request" || u.Merge == nil || u.Merge.Head != head || !strings.Contains(u.Body, "squash") {
		t.Fatalf("request %+v", u)
	}
}

func TestMergedOutcomeCompletesTaskAndFreesLock(t *testing.T) {
	s := service(t)
	task, a, claimed, lock := inReview(t, s)
	first := call(t, s, a, ready(task.ID, lock.Token, good())).Updates[0]
	second := call(t, s, a, ready(task.ID, lock.Token, good())).Updates[0]
	st := mustSnapshot(t, s)
	if got, ok := PendingMerge(&st, task.ID); !ok || got.ID != second.ID {
		t.Fatalf("pending %+v %v, want the newest request %d", got, ok, second.ID)
	}
	reject(t, s, a, Request{Op: "merge-outcome", ReplyTo: second.ID, State: MergeMerged, Body: "merged"}, 403)
	reject(t, s, coordinator, Request{Op: "merge-outcome", ReplyTo: first.ID - 1, State: MergeMerged, Body: "merged"}, 404)
	reject(t, s, coordinator, Request{Op: "merge-outcome", ReplyTo: second.ID, State: "maybe", Body: "?"}, 400)
	out := call(t, s, coordinator, Request{Op: "merge-outcome", ReplyTo: second.ID, State: MergeMerged, Body: "Merged at " + head})
	if out.Item.State != "done" || out.Item.PR != good().PR {
		t.Fatalf("item %+v", out.Item)
	}
	reject(t, s, coordinator, Request{Op: "merge-outcome", ReplyTo: second.ID, State: MergeMerged, Body: "again"}, 409)
	st = mustSnapshot(t, s)
	if _, held := st.Locks[task.Repo]; held {
		t.Fatal("merge kept the lock")
	}
	if _, ok := PendingMerge(&st, task.ID); ok {
		t.Fatal("answered request still pending")
	}
	if st.Attempts[claimed.Attempt.ID].State == "ended" {
		t.Fatal("the merge ended the attempt; the executor still reports and cleans up")
	}
}

func TestRefusedOutcomeKeepsTaskAndLock(t *testing.T) {
	s := service(t)
	task, a, _, lock := inReview(t, s)
	req := call(t, s, a, ready(task.ID, lock.Token, good())).Updates[0]
	reject(t, s, coordinator, Request{Op: "merge-outcome", ReplyTo: req.ID, State: MergeRefused}, 400)
	out := call(t, s, coordinator, Request{Op: "merge-outcome", ReplyTo: req.ID, State: MergeRefused, Body: "The default branch moved"})
	if out.Item.State != "in-review" || out.Updates[0].ReplyTo != req.ID {
		t.Fatalf("refusal %+v", out)
	}
	st := mustSnapshot(t, s)
	if st.Locks[task.Repo].Token != lock.Token {
		t.Fatal("refusal released the executor's lock")
	}
	if _, ok := PendingMerge(&st, task.ID); ok {
		t.Fatal("refused request still pending")
	}
}

func TestPRNumber(t *testing.T) {
	pull := "https://github.com/o/r/" + "pull/"
	for url, want := range map[string]int{pull + "42": 42, pull + "0": 0, pull + "x": 0, "https://github.com/o/r": 0} {
		if got := PRNumber(url); got != want {
			t.Errorf("PRNumber(%q) = %d, want %d", url, got, want)
		}
	}
}

func TestMergeFailuresHoldTheRequestUntilAcknowledged(t *testing.T) {
	s := service(t)
	task, a, _, lock := inReview(t, s)
	req := call(t, s, a, ready(task.ID, lock.Token, good())).Updates[0]
	st := mustSnapshot(t, s)
	if n, held := MergeFailures(&st, req.ID); n != 0 || held || !MergedAt(&st, task.ID).IsZero() {
		t.Fatal("a fresh request is held or merged")
	}
	notice := call(t, s, coordinator, Request{Op: "say", ID: task.ID, ReplyTo: req.ID, Recipient: "operator", Body: "gh failed"}).Updates[0]
	st = mustSnapshot(t, s)
	if n, held := MergeFailures(&st, req.ID); n != 1 || !held {
		t.Fatalf("failures %d, held %v", n, held)
	}
	call(t, s, op, Request{Op: "ack", ReplyTo: notice.ID})
	call(t, s, coordinator, Request{Op: "merge-outcome", ReplyTo: req.ID, State: MergeMerged, Body: "merged"})
	st = mustSnapshot(t, s)
	if n, held := MergeFailures(&st, req.ID); n != 1 || held || MergedAt(&st, task.ID).IsZero() {
		t.Fatalf("failures %d, held %v after acknowledgement and merge", n, held)
	}
}
