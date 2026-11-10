package controlrun

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/reconcile"
	"github.com/dinoderek/engine/internal/remote"
)

// refusedOnce checks that task's entry ended with a merge-refused update
// whose body has want, and that the operator was told once.
func refusedOnce(t *testing.T, c *Coordinator, id, want string) {
	t.Helper()
	if e, ok := entry(t, c, id); ok {
		t.Fatalf("still queued: %+v", e)
	}
	u, ok := lastUpdate(t, c, id, control.MergeRefused)
	if notices := operatorNotices(t, c, id); !ok || !strings.Contains(u.Body, want) || len(notices) != 1 {
		t.Fatalf("refusal %+v; %d operator notices", u, len(notices))
	}
	st, _ := c.Service.Store.Snapshot()
	if st.Items[id].State != "in-review" {
		t.Fatalf("task %s after the refusal", st.Items[id].State)
	}
}

func TestAHeadThatDoesNotRefreshIsRefused(t *testing.T) {
	c, _, task, _ := requested(t, reply(hostops.CodeBehind, "main moved"))
	pass(t, c) // its turn
	later(c, reconcile.TurnWait)
	pass(t, c)
	refusedOnce(t, c, task.ID, "not refreshed")
}

func TestChecksPendingTooLongRefuseTheHead(t *testing.T) {
	c, _, task, _ := requested(t, reply(hostops.CodePendingChecks, "check test is still running"))
	pass(t, c)
	later(c, reconcile.ChecksWait)
	pass(t, c)
	refusedOnce(t, c, task.ID, "check test is still running")
}

func TestMergeabilityUnknownTooLongRefusesTheHead(t *testing.T) {
	c, _, task, _ := requested(t, reply(hostops.CodePendingMergeable, "unknown"))
	pass(t, c)
	later(c, reconcile.MergeableWait)
	pass(t, c)
	refusedOnce(t, c, task.ID, "mergeable")
}

func TestRepeatedGhFailuresRefuseTheHead(t *testing.T) {
	c, m, task, _ := requested(t, func(hostops.MergeRequest) (hostops.Merged, error) {
		return hostops.Merged{}, errors.New("gh: authentication required")
	})
	for range reconcile.GhFailures + 2 {
		pass(t, c)
		later(c, reconcile.CheckEvery)
	}
	if len(m.merges()) != reconcile.GhFailures {
		t.Fatalf("%d calls, want %d", len(m.merges()), reconcile.GhFailures)
	}
	refusedOnce(t, c, task.ID, "gh auth status")
}

func TestAnUnreachableHostLeavesTheMergeUnknownThenRefusesIt(t *testing.T) {
	c, _, task, _ := requested(t, func(hostops.MergeRequest) (hostops.Merged, error) {
		return hostops.Merged{}, &remote.UnreachableError{Host: "devbox", Address: "devbox", Detail: "timeout"}
	})
	pass(t, c)
	st, _ := c.Service.Store.Snapshot()
	if e := st.Queue[task.ID]; e.FailSince.IsZero() || len(operatorNotices(t, c, task.ID)) != 0 || st.Attempts[st.Items[task.ID].Attempt].State == "ended" {
		t.Fatalf("an outage acted: entry %+v", e)
	}
	later(c, reconcile.HostWait)
	pass(t, c)
	refusedOnce(t, c, task.ID, "unreachable")
}

func TestAHostMissingFromTheConfigRefusesTheHead(t *testing.T) {
	c, m, task, _ := requested(t, reply(hostops.CodeMerged, "merged"))
	if err := c.Service.Store.Transaction(func(st *control.State) error {
		at := st.Attempts[st.Items[task.ID].Attempt]
		at.Host = "gone"
		st.Attempts[at.ID] = at
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.queues(c.Config); err != nil {
		t.Fatal(err)
	}
	if len(m.requests) != 0 {
		t.Fatalf("calls %+v", m.requests)
	}
	refusedOnce(t, c, task.ID, "hosts.toml")
}

// queuedTask adds task id of plan, in-review on devbox with a live attempt,
// to repo's merge queue behind the others.
func queuedTask(t *testing.T, c *Coordinator, repo, plan, id string) {
	t.Helper()
	if err := c.Service.Store.Transaction(func(st *control.State) error {
		at := "a" + id[1:]
		st.Items[id] = control.Item{ID: id, Kind: "task", Repo: repo, Plan: plan, Title: id, State: "in-review", PR: reviewedPR, Attempt: at, Version: 1}
		st.Attempts[at] = control.Attempt{ID: at, Item: id, Host: "devbox", State: "running"}
		u := control.Update{ID: len(st.Updates) + 1, Item: id, Author: at, Kind: "merge-request",
			Merge: &control.MergeRequest{PR: reviewedPR, Head: reviewedHead, Method: "squash", Verdict: "APPROVED"}}
		st.Updates = append(st.Updates, u)
		st.Queue[id] = control.QueueEntry{Task: id, Repo: repo, Request: u.ID, PR: reviewedPR, Head: reviewedHead, Seq: u.ID, Phase: control.QueueWaiting}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestOneMergeInFlightPerRepositoryInRequestOrder(t *testing.T) {
	c, m, task, _ := requested(t, reply(hostops.CodePendingChecks, "running"))
	queuedTask(t, c, task.Repo, task.Plan, "t90")
	if err := c.queues(c.Config); err != nil {
		t.Fatal(err)
	}
	if len(m.requests) != 1 || m.requests[0].Head != reviewedHead {
		t.Fatalf("calls %+v: want the head alone called", m.requests)
	}
	// The head's host keeps failing: it is refused, and on the next pass the
	// next entry becomes head and is called at once.
	m.reply = func(r hostops.MergeRequest) (hostops.Merged, error) {
		if len(m.requests) <= 1+reconcile.GhFailures {
			return hostops.Merged{}, errors.New("gh: authentication required")
		}
		return hostops.Merged{Outcome: hostops.MergeDone, Code: hostops.CodeMerged, Reason: "merged"}, nil
	}
	for range reconcile.GhFailures {
		later(c, reconcile.CheckEvery)
		if err := c.queues(c.Config); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.queues(c.Config); err != nil {
		t.Fatal(err)
	}
	st, _ := c.Service.Store.Snapshot()
	if _, queued := st.Queue[task.ID]; queued || st.Items["t90"].State != "done" || len(m.requests) != 2+reconcile.GhFailures {
		t.Fatalf("t90 %s, head queued %v, %d calls", st.Items["t90"].State, queued, len(m.requests))
	}
}

// A head waiting on checks is called once per interval, and at once when
// its executor requests again; the waiting entry behind it is never called.
func TestTheHeadIsCalledOncePerIntervalAndAtOnceOnANewRequest(t *testing.T) {
	c, m, task, worker := requested(t, reply(hostops.CodePendingChecks, "running"))
	queuedTask(t, c, task.Repo, task.Plan, "t90")
	pass(t, c)
	pass(t, c)
	later(c, reconcile.CheckEvery-time.Second)
	pass(t, c)
	if len(m.requests) != 1 {
		t.Fatalf("%d calls within one interval, want 1", len(m.requests))
	}
	later(c, time.Second)
	pass(t, c)
	if len(m.requests) != 2 {
		t.Fatalf("%d calls once the interval passed, want 2", len(m.requests))
	}
	request(t, c, worker, task.ID, strings.Repeat("b", 40))
	pass(t, c)
	if len(m.requests) != 3 || m.requests[2].Head != strings.Repeat("b", 40) {
		t.Fatalf("calls %+v: want the new request called at once", m.requests)
	}
}

// The queue rereads the server just before a host call: a question asked
// after the pass decided to merge holds the merge instead.
func TestAMergeRereadsTheServerBeforeTheHostCall(t *testing.T) {
	c2, m2, task2, worker2 := requested(t, reply(hostops.CodeMerged, "merged"))
	v, err := c2.queueView(task2.Repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c2.steps(reconcile.DecideQueue(v)); err != nil {
		t.Fatal(err)
	}
	v, _ = c2.queueView(task2.Repo)
	acts := reconcile.DecideQueue(v)
	if len(acts) != 1 || acts[0].Kind != reconcile.Merge {
		t.Fatalf("actions %+v", acts)
	}
	if _, err := c2.Service.Execute(worker2, control.Request{Op: "ask", ID: task2.ID, Body: "Wait", Key: "ask"}); err != nil {
		t.Fatal(err)
	}
	if err := c2.call(task2.Repo, acts[0], c2.Config); err != nil {
		t.Fatal(err)
	}
	if len(m2.requests) != 0 {
		t.Fatalf("merged after the question: %+v", m2.requests)
	}
}

// A merge that lands while the executor withdraws the task is still
// recorded, and the operator is told of the race.
func TestAMergeRacingAWithdrawalIsRecorded(t *testing.T) {
	c, m, task, worker := requested(t, nil)
	m.reply = func(r hostops.MergeRequest) (hostops.Merged, error) {
		st, _ := c.Service.Store.Snapshot()
		if _, err := c.Service.Execute(worker, control.Request{Op: "state", ID: task.ID, Version: st.Items[task.ID].Version, State: "running", Key: control.Secret()}); err != nil {
			t.Fatal(err)
		}
		return hostops.Merged{Outcome: hostops.MergeDone, Code: hostops.CodeMerged, Reason: "merged at " + reviewedHead}, nil
	}
	if err := c.queues(c.Config); err != nil {
		t.Fatal(err)
	}
	st, _ := c.Service.Store.Snapshot()
	if st.Items[task.ID].State != "done" || st.Items[task.ID].PR != reviewedPR || len(operatorNotices(t, c, task.ID)) != 1 {
		t.Fatalf("task %+v, notices %d", st.Items[task.ID], len(operatorNotices(t, c, task.ID)))
	}
}

// A head whose session vanishes during its turn is resumed in-review, and
// its turn's bound still refuses it.
func TestAResumedHeadKeepsItsTurnsBound(t *testing.T) {
	c, m, task, _ := requested(t, reply(hostops.CodeBehind, "main moved"))
	pass(t, c) // its turn; the window was seen live
	m.status.Windows = nil
	pass(t, c) // vanished
	pass(t, c) // resumed
	st, _ := c.Service.Store.Snapshot()
	if at := st.Attempts[st.Items[task.ID].Attempt]; at.State == "ended" || st.Items[task.ID].State != "in-review" || st.Queue[task.ID].Phase != control.QueueRefresh {
		t.Fatalf("after the resume: task %s, attempt %+v, entry %+v", st.Items[task.ID].State, at, st.Queue[task.ID])
	}
	later(c, reconcile.TurnWait)
	pass(t, c)
	if _, ok := entry(t, c, task.ID); ok {
		t.Fatal("still queued after its turn's bound")
	}
	if _, ok := lastUpdate(t, c, task.ID, control.MergeRefused); !ok {
		t.Fatal("not refused")
	}
}
