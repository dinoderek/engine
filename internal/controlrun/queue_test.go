package controlrun

import (
	"errors"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/reconcile"
	"github.com/dinoderek/engine/internal/remote"
)

func TestAHeadThatDoesNotRefreshIsDemotedThenDropped(t *testing.T) {
	c, _, task, _ := requested(t, reply(hostops.CodeBehind, "main moved"))
	for i := 1; i < reconcile.MaxDemotions; i++ {
		pass(t, c) // its turn
		later(c, reconcile.TurnWait)
		pass(t, c)
		if e, ok := entry(t, c, task.ID); !ok || e.Demotions != i {
			t.Fatalf("after %d turns: %+v", i, e)
		}
		if _, ok := lastUpdate(t, c, task.ID, control.MergeDemoted); !ok {
			t.Fatal("the executor was not told it was demoted")
		}
	}
	pass(t, c)
	later(c, reconcile.TurnWait)
	pass(t, c)
	if _, ok := entry(t, c, task.ID); ok {
		t.Fatal("still queued after the last demotion")
	}
	if u, ok := lastUpdate(t, c, task.ID, control.MergeRefused); !ok || !strings.Contains(u.Body, "Dropped") || len(operatorNotices(t, c, task.ID)) != reconcile.MaxDemotions {
		t.Fatalf("drop %+v; notices %d", u, len(operatorNotices(t, c, task.ID)))
	}
}

func TestChecksPendingTooLongDemoteTheHead(t *testing.T) {
	c, _, task, _ := requested(t, reply(hostops.CodePendingChecks, "check test is still running"))
	pass(t, c)
	later(c, reconcile.ChecksWait)
	pass(t, c)
	if e, _ := entry(t, c, task.ID); e.Demotions != 1 || len(operatorNotices(t, c, task.ID)) != 1 {
		t.Fatalf("entry %+v", e)
	}
}

func TestRepeatedGhFailuresHoldTheHeadUntilTheOperatorAcknowledges(t *testing.T) {
	c, m, task, _ := requested(t, func(hostops.MergeRequest) (hostops.Merged, error) {
		return hostops.Merged{}, errors.New("gh: authentication required")
	})
	for range reconcile.GhFailures + 2 {
		pass(t, c)
	}
	notices := operatorNotices(t, c, task.ID)
	e, _ := entry(t, c, task.ID)
	if len(notices) != 1 || !strings.Contains(notices[0].Body, "gh auth status") || len(m.merges()) != reconcile.GhFailures || e.Held != notices[0].ID {
		t.Fatalf("%d notices after %d merges; entry %+v", len(notices), len(m.merges()), e)
	}
	acknowledge(t, c, notices[0])
	pass(t, c)
	if len(m.merges()) != reconcile.GhFailures+1 {
		t.Fatalf("%d merges once acknowledged", len(m.merges()))
	}
}

func TestAnUnreachableHostLeavesTheMergeUnknownThenDemotesIt(t *testing.T) {
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
	if e, _ := entry(t, c, task.ID); e.Demotions != 1 || len(operatorNotices(t, c, task.ID)) != 1 {
		t.Fatalf("after %s: %+v", reconcile.HostWait, e)
	}
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
	if got := m.merges(); len(got) != 1 || len(m.requests) != 2 || !m.requests[1].Check {
		t.Fatalf("calls %+v: want the head merged and the next only read", m.requests)
	}
	// The head's host keeps failing: it is held, and the next entry merges.
	m.reply = func(r hostops.MergeRequest) (hostops.Merged, error) {
		if len(m.merges()) <= 1+reconcile.GhFailures {
			return hostops.Merged{}, errors.New("gh: authentication required")
		}
		return hostops.Merged{Outcome: hostops.MergeDone, Code: hostops.CodeMerged, Reason: "merged"}, nil
	}
	for range reconcile.GhFailures + 1 {
		if err := c.queues(c.Config); err != nil {
			t.Fatal(err)
		}
	}
	st, _ := c.Service.Store.Snapshot()
	if st.Items["t90"].State != "done" || !control.QueueHeld(&st, st.Queue[task.ID]) {
		t.Fatalf("t90 %s, head %+v", st.Items["t90"].State, st.Queue[task.ID])
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

func acknowledge(t *testing.T, c *Coordinator, u control.Update) {
	t.Helper()
	if _, err := c.Service.Execute(control.Actor{Name: "operator", Role: "operator"}, control.Request{Op: "ack", ReplyTo: u.ID, Key: control.Secret()}); err != nil {
		t.Fatal(err)
	}
}

// A merge that lands while the executor withdraws the task is still
// recorded, and the operator is told of the race.
func TestAMergeRacingAWithdrawalIsRecorded(t *testing.T) {
	c, m, task, worker := requested(t, nil)
	m.reply = func(r hostops.MergeRequest) (hostops.Merged, error) {
		if !r.Check {
			st, _ := c.Service.Store.Snapshot()
			if _, err := c.Service.Execute(worker, control.Request{Op: "state", ID: task.ID, Version: st.Items[task.ID].Version, State: "running", Key: control.Secret()}); err != nil {
				t.Fatal(err)
			}
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
// its turn's bound still demotes it.
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
	if e, _ := entry(t, c, task.ID); e.Demotions != 1 {
		t.Fatalf("entry %+v", e)
	}
}

func TestAHostMissingFromTheConfigHoldsTheHead(t *testing.T) {
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
	notices := operatorNotices(t, c, task.ID)
	if e, _ := entry(t, c, task.ID); len(notices) != 1 || !strings.Contains(notices[0].Body, "hosts.toml") || e.Held != notices[0].ID || len(m.requests) != 0 {
		t.Fatalf("entry %+v, notices %+v", e, notices)
	}
}
