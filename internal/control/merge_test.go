package control

import (
	"strings"
	"testing"
	"time"
)

const head = "0123456789abcdef0123456789abcdef01234567"

// inReview dispatches a new task of plan and moves it to in-review.
func inReview(t *testing.T, s Service, repo, plan Item) (Item, Actor, Response) {
	t.Helper()
	task := newTask(t, s, repo.ID, plan.ID)
	if plan.ApprovedVersion == 0 {
		approve(t, s, plan)
	}
	a, claimed := dispatch(t, s, task)
	call(t, s, a, Request{Op: "state", ID: task.ID, Version: claimed.Item.Version, State: "in-review", Item: Item{PR: good().PR}})
	return task, a, claimed
}

// reviewed is one in-review task in a new plan.
func reviewed(t *testing.T, s Service) (Item, Actor, Response) {
	t.Helper()
	repo, plan := repoPlan(t, s)
	return inReview(t, s, repo, plan)
}

func ready(id string, m MergeRequest) Request {
	return Request{Op: "merge-request", ID: id, Merge: &m}
}

func good() MergeRequest {
	return MergeRequest{PR: externalPR("owner/demo"), Head: head, Method: "squash", Verdict: "APPROVED"}
}

func TestMergeRequestValidation(t *testing.T) {
	s := service(t)
	task, a, _ := reviewed(t, s)
	bad := func(edit func(*MergeRequest)) MergeRequest { m := good(); edit(&m); return m }
	for name, c := range map[string]struct {
		r      Request
		status int
	}{
		"no details":      {Request{Op: "merge-request", ID: task.ID}, 400},
		"other repo PR":   {ready(task.ID, bad(func(m *MergeRequest) { m.PR = externalPR("x/y") })), 400},
		"short head":      {ready(task.ID, bad(func(m *MergeRequest) { m.Head = "0123abc" })), 400},
		"unknown method":  {ready(task.ID, bad(func(m *MergeRequest) { m.Method = "admin" })), 400},
		"not approved":    {ready(task.ID, bad(func(m *MergeRequest) { m.Verdict = "CHANGES_REQUESTED" })), 409},
		"another PR":      {ready(task.ID, bad(func(m *MergeRequest) { m.PR = externalPR("owner/demo") + "3" })), 409},
		"another task":    {ready("t99", good()), 403},
		"operator cannot": {ready(task.ID, good()), 403},
	} {
		actor := a
		if name == "operator cannot" {
			actor = op
		}
		t.Run(name, func(t *testing.T) { reject(t, s, actor, c.r, c.status) })
	}
	st := mustSnapshot(t, s)
	if _, ok := PendingMerge(&st, task.ID); ok || len(st.Queue) != 0 {
		t.Fatal("a refused request was queued")
	}
}

func TestMergeRequestNeedsInReviewAndNoQuestion(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	task := newTask(t, s, repo.ID, plan.ID)
	approve(t, s, plan)
	a, claimed := dispatch(t, s, task)
	reject(t, s, a, ready(task.ID, good()), 409) // running
	call(t, s, a, Request{Op: "state", ID: task.ID, Version: claimed.Item.Version, State: "in-review", Item: Item{PR: good().PR}})
	question := call(t, s, a, Request{Op: "ask", ID: task.ID, Body: "Approve the merge?"}).Updates[0]
	reject(t, s, a, ready(task.ID, good()), 409)
	call(t, s, op, Request{Op: "resolve", ReplyTo: question.ID, Body: "Approved"})
	current := call(t, s, op, Request{Op: "get", ID: task.ID}).Item
	call(t, s, op, Request{Op: "state", ID: task.ID, Version: current.Version, State: "in-review"})
	out := call(t, s, a, ready(task.ID, good()))
	if u := out.Updates[0]; u.Kind != "merge-request" || u.Merge == nil || u.Merge.Head != head || !strings.Contains(u.Body, "squash") {
		t.Fatalf("request %+v", u)
	}
	if len(out.Queue) != 1 || out.Queue[0].Phase != QueueWaiting || out.Queue[0].Seq != out.Updates[0].ID {
		t.Fatalf("queue entry %+v", out.Queue)
	}
}

func TestMergeRequestsQueueInOrderAndKeepTheirPlace(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	one, a, _ := inReview(t, s, repo, plan)
	plan = *call(t, s, op, Request{Op: "get", ID: plan.ID}).Item
	two, b, _ := inReview(t, s, repo, plan)
	first := call(t, s, a, ready(one.ID, good())).Updates[0]
	call(t, s, b, ready(two.ID, good()))
	// The head is told to refresh; its new request keeps its place and is checked again.
	st := mustSnapshot(t, s)
	from := st.Queue[one.ID]
	to := from
	to.Phase, to.TurnAt, to.WaitSince = QueueRefresh, s.now(), s.now()
	if ok, err := s.QueueStep(QueueStep{From: from, To: &to, Kind: MergeTurn, Text: "Your turn"}); err != nil || !ok {
		t.Fatal(ok, err)
	}
	m := good()
	m.Head = strings.Repeat("b", 40)
	again := call(t, s, a, ready(one.ID, m)).Updates[0]
	queue := call(t, s, op, Request{Op: "merge-queue", ID: repo.ID}).Queue
	if len(queue) != 2 || queue[0].Task != one.ID || queue[1].Task != two.ID {
		t.Fatalf("queue %+v", queue)
	}
	if e := queue[0]; e.Seq != first.ID || e.Request != again.ID || e.Head != m.Head || e.Phase != QueueCheck || !e.TurnAt.IsZero() || !e.WaitSince.IsZero() {
		t.Fatalf("re-requested entry %+v", e)
	}
	if got := call(t, s, b, Request{Op: "get", ID: two.ID}).Queue; len(got) != 1 || got[0].Task != two.ID {
		t.Fatalf("task show queue %+v", got)
	}
	if got := call(t, s, b, Request{Op: "merge-queue", ID: repo.ID}).Queue; len(got) != 2 {
		t.Fatalf("a worker reads its repository's queue: %+v", got)
	}
	other := *call(t, s, op, Request{Op: "create", Item: Item{Kind: "repo", Title: "Other", Slug: "owner/other", Remote: "git@example.test:owner/other.git"}}).Item
	reject(t, s, b, Request{Op: "merge-queue", ID: other.ID}, 403)
	reject(t, s, op, Request{Op: "merge-queue", ID: plan.ID}, 404)
}

func TestQueueStepAppliesOnlyToTheEntryItRead(t *testing.T) {
	s := service(t)
	task, a, claimed := reviewed(t, s)
	req := call(t, s, a, ready(task.ID, good())).Updates[0]
	from := mustSnapshot(t, s).Queue[task.ID]
	stale := from
	stale.Phase = QueueCheck
	if ok, err := s.QueueStep(QueueStep{From: stale, Done: true, Kind: MergeMerged, Text: "Merged"}); err != nil || ok {
		t.Fatalf("a stale step applied: %v %v", ok, err)
	}
	demoted := from
	demoted.Demotions = 1
	if ok, err := s.QueueStep(QueueStep{From: from, To: &demoted, Kind: MergeDemoted, Text: "back", Demote: true, Notice: "moved back"}); err != nil || !ok {
		t.Fatal(ok, err)
	}
	st := mustSnapshot(t, s)
	e := st.Queue[task.ID]
	last := st.Updates[len(st.Updates)-1]
	if e.Seq != last.ID-1 || e.Demotions != 1 || last.Recipient != "operator" || last.ReplyTo != req.ID || st.Updates[last.ID-2].Kind != MergeDemoted {
		t.Fatalf("demoted entry %+v, updates %+v", e, st.Updates[len(st.Updates)-2:])
	}
	held := e
	if ok, err := s.QueueStep(QueueStep{From: e, To: &held, Hold: true, Notice: "gh failed"}); err != nil || !ok {
		t.Fatal(ok, err)
	}
	st = mustSnapshot(t, s)
	e = st.Queue[task.ID]
	if !QueueHeld(&st, e) {
		t.Fatalf("hold %+v", e)
	}
	call(t, s, op, Request{Op: "ack", ReplyTo: e.Held})
	st = mustSnapshot(t, s)
	if QueueHeld(&st, e) || !MergedAt(&st, task.ID).IsZero() {
		t.Fatal("still held after the acknowledgement, or merged")
	}
	if ok, err := s.QueueStep(QueueStep{From: e, Done: true, Kind: MergeMerged, Text: "Merged at " + head}); err != nil || !ok {
		t.Fatal(ok, err)
	}
	st = mustSnapshot(t, s)
	if got := st.Items[task.ID]; got.State != "done" || got.PR != good().PR || len(st.Queue) != 0 || MergedAt(&st, task.ID).IsZero() {
		t.Fatalf("task %+v, queue %+v", got, st.Queue)
	}
	if st.Attempts[claimed.Attempt.ID].State == "ended" {
		t.Fatal("the merge ended the attempt; the executor still reports")
	}
}

func TestLeavingInReviewLeavesTheQueueExceptForAHardStop(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	task, a, _ := inReview(t, s, repo, plan)
	call(t, s, a, ready(task.ID, good()))
	call(t, s, a, Request{Op: "ask", ID: task.ID, Body: "Approve?"})
	st := mustSnapshot(t, s)
	if _, ok := st.Queue[task.ID]; !ok || st.Items[task.ID].State != "needs-human" {
		t.Fatal("a hard stop took the task out of the queue")
	}
	plan = *call(t, s, op, Request{Op: "get", ID: plan.ID}).Item
	rework, b, _ := inReview(t, s, repo, plan)
	req := call(t, s, b, ready(rework.ID, good())).Updates[0]
	current := call(t, s, b, Request{Op: "get", ID: rework.ID}).Item
	call(t, s, b, Request{Op: "state", ID: rework.ID, Version: current.Version, State: "running"})
	st = mustSnapshot(t, s)
	last := st.Updates[len(st.Updates)-2]
	if _, ok := st.Queue[rework.ID]; ok || last.Kind != MergeWithdrawn || last.ReplyTo != req.ID {
		t.Fatalf("rework stayed queued; update %+v", last)
	}
	failing, c, claimed := inReview(t, s, repo, plan)
	call(t, s, c, ready(failing.ID, good()))
	if _, err := s.EndDead(claimed.Attempt.ID, "window gone"); err != nil {
		t.Fatal(err)
	}
	st = mustSnapshot(t, s)
	if _, ok := st.Queue[failing.ID]; ok || st.Items[failing.ID].State != "failed" {
		t.Fatalf("a failed task stayed queued: %s", st.Items[failing.ID].State)
	}
}

func TestAVanishedTaskKeepsItsPlace(t *testing.T) {
	s := service(t)
	task, a, claimed := reviewed(t, s)
	call(t, s, a, ready(task.ID, good()))
	if err := s.Store.Transaction(func(st *State) error {
		at := st.Attempts[claimed.Attempt.ID]
		at.LiveAt = time.Date(2026, 11, 7, 8, 0, 0, 0, time.UTC)
		st.Attempts[at.ID] = at
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if at, err := s.EndDead(claimed.Attempt.ID, "window gone"); err != nil || at.Reason != EndVanished {
		t.Fatal(at, err)
	}
	st := mustSnapshot(t, s)
	if u, ok := PendingMerge(&st, task.ID); !ok || u.Merge.Head != head {
		t.Fatal("a vanished task left the queue")
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
