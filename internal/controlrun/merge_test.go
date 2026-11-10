package controlrun

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hook"
	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/reconcile"
	"github.com/dinoderek/engine/internal/remote"
)

const reviewedHead = "0123456789abcdef0123456789abcdef01234567"

var reviewedPR = fmt.Sprintf("https://github.com/owner/demo/pull/%d", 42)

// merger is a host whose merge op answers with reply, recording each request.
type merger struct {
	*host
	reply    func(hostops.MergeRequest) (hostops.Merged, error)
	requests []hostops.MergeRequest
}

// ControlMerge answers as the host does.
func (m *merger) ControlMerge(r hostops.MergeRequest) (hostops.Merged, error) {
	m.requests = append(m.requests, r)
	return m.reply(r)
}

// merges are the host calls made.
func (m *merger) merges() []hostops.MergeRequest { return m.requests }

// requested dispatches the task, then has its executor move it to in-review
// and request the merge.
func requested(t *testing.T, reply func(hostops.MergeRequest) (hostops.Merged, error)) (*Coordinator, *merger, control.Item, control.Actor) {
	t.Helper()
	c, h, _, _, task := setup(t)
	if reply == nil {
		reply = func(hostops.MergeRequest) (hostops.Merged, error) { return hostops.Merged{}, nil }
	}
	m := &merger{host: h, reply: reply}
	c.Client = func(hosts.Host) remote.Client { return m }
	pass(t, c)
	pass(t, c)
	st, _ := c.Service.Store.Snapshot()
	worker, err := c.Service.Authenticate(m.launches[st.Attempts[st.Items[task.ID].Attempt].RunID].Credential, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Service.Execute(worker, control.Request{Op: "state", ID: task.ID, Version: st.Items[task.ID].Version, State: "in-review",
		Item: control.Item{PR: reviewedPR}, Key: control.Secret()}); err != nil {
		t.Fatal(err)
	}
	request(t, c, worker, task.ID, reviewedHead)
	return c, m, task, worker
}

func request(t *testing.T, c *Coordinator, worker control.Actor, id, head string) {
	t.Helper()
	if _, err := c.Service.Execute(worker, control.Request{Op: "merge-request", ID: id, Key: control.Secret(), Merge: &control.MergeRequest{
		PR: reviewedPR, Head: head, Method: "squash", Verdict: "APPROVED"}}); err != nil {
		t.Fatal(err)
	}
}

func reply(code, reason string) func(hostops.MergeRequest) (hostops.Merged, error) {
	outcome := map[string]string{hostops.CodeMerged: hostops.MergeDone, hostops.CodePendingChecks: hostops.MergePending,
		hostops.CodePendingMergeable: hostops.MergePending}[code]
	if outcome == "" {
		outcome = hostops.MergeRefused
	}
	return func(hostops.MergeRequest) (hostops.Merged, error) {
		return hostops.Merged{Outcome: outcome, Code: code, Reason: reason}, nil
	}
}

func lastUpdate(t *testing.T, c *Coordinator, id string, kinds ...string) (control.Update, bool) {
	t.Helper()
	st, _ := c.Service.Store.Snapshot()
	for i := len(st.Updates) - 1; i >= 0; i-- {
		u := st.Updates[i]
		for _, k := range kinds {
			if u.Item == id && u.Kind == k {
				return u, true
			}
		}
	}
	return control.Update{}, false
}

// operatorNotices are the coordinator's messages to the operator on id.
func operatorNotices(t *testing.T, c *Coordinator, id string) []control.Update {
	t.Helper()
	st, _ := c.Service.Store.Snapshot()
	var out []control.Update
	for _, u := range st.Updates {
		if u.Item == id && u.Recipient == "operator" && u.Author == "coordinator" {
			out = append(out, u)
		}
	}
	return out
}

func entry(t *testing.T, c *Coordinator, id string) (control.QueueEntry, bool) {
	t.Helper()
	st, _ := c.Service.Store.Snapshot()
	e, ok := st.Queue[id]
	return e, ok
}

func later(c *Coordinator, d time.Duration) {
	now := c.Service.Now().Add(d)
	c.Service.Now = func() time.Time { return now }
}

func TestCoordinatorMergesOnTheTasksHostAndCompletesTheTask(t *testing.T) {
	c, m, task, _ := requested(t, reply(hostops.CodeMerged, "merged at "+reviewedHead))
	var woken []string
	c.Wake = func(_ reconcile.Session, text string) error { woken = append(woken, text); return nil }
	m.status.Windows[0].State = "waiting"
	pass(t, c)
	if got := m.merges(); len(got) != 1 || got[0] != (hostops.MergeRequest{Repo: "owner/demo", PR: 42, Head: reviewedHead, Method: "squash"}) {
		t.Fatalf("merge requests %+v", m.requests)
	}
	st, _ := c.Service.Store.Snapshot()
	if got := st.Items[task.ID]; got.State != "done" || got.PR != reviewedPR || len(st.Queue) != 0 {
		t.Fatalf("task %+v, queue %+v", got, st.Queue)
	}
	if len(woken) == 0 {
		t.Fatal("the executor was not told about its merge")
	}
	pass(t, c)
	if len(m.requests) != 1 {
		t.Fatal("a merged task was merged again")
	}
}

func TestCoordinatorHandsBackARedOrClosedPR(t *testing.T) {
	for code, operator := range map[string]bool{hostops.CodeRed: false, hostops.CodeClosed: true, hostops.CodeBlocked: true} {
		t.Run(code, func(t *testing.T) {
			c, m, task, _ := requested(t, reply(code, "the reason"))
			woken := 0
			c.Wake = func(reconcile.Session, string) error { woken++; return nil }
			m.status.Windows[0].State = "waiting"
			pass(t, c)
			u, ok := lastUpdate(t, c, task.ID, control.MergeRefused)
			st, _ := c.Service.Store.Snapshot()
			if !ok || !strings.Contains(u.Body, "the reason") || st.Items[task.ID].State != "in-review" || len(st.Queue) != 0 || woken == 0 {
				t.Fatalf("refusal %+v, task %s, queue %+v, woken %d", u, st.Items[task.ID].State, st.Queue, woken)
			}
			if told := len(operatorNotices(t, c, task.ID)) > 0; told != operator {
				t.Errorf("operator told %v, want %v", told, operator)
			}
		})
	}
}

func TestABehindHeadGetsItsTurnAndMergesOnceRefreshed(t *testing.T) {
	c, m, task, worker := requested(t, reply(hostops.CodeBehind, "main moved: 2 commits behind"))
	woken := 0
	c.Wake = func(reconcile.Session, string) error { woken++; return nil }
	m.status.Windows[0].State = "waiting"
	pass(t, c)
	turn, ok := lastUpdate(t, c, task.ID, control.MergeTurn)
	if e, _ := entry(t, c, task.ID); !ok || e.Phase != control.QueueRefresh || !strings.Contains(turn.Body, "main moved") || woken != 1 {
		t.Fatalf("turn %+v, entry %+v, woken %d", turn, e, woken)
	}
	m.status.Windows[0].State = "working"
	pass(t, c)
	if len(m.merges()) != 1 {
		t.Fatal("a refreshing head was merged before it requested again")
	}
	refreshed := strings.Repeat("b", 40)
	request(t, c, worker, task.ID, refreshed)
	m.reply = reply(hostops.CodeMerged, "merged at "+refreshed)
	pass(t, c)
	st, _ := c.Service.Store.Snapshot()
	if got := m.merges(); len(got) != 2 || got[1].Head != refreshed || st.Items[task.ID].State != "done" {
		t.Fatalf("merges %+v, task %s", got, st.Items[task.ID].State)
	}
}

func TestPendingChecksWaitAndAQuestionHoldsTheMerge(t *testing.T) {
	c, m, task, worker := requested(t, reply(hostops.CodePendingChecks, "check test is still running"))
	pass(t, c)
	if _, ok := lastUpdate(t, c, task.ID, control.MergeMerged, control.MergeRefused); ok || len(m.merges()) != 1 {
		t.Fatal("pending checks were answered")
	}
	if _, err := c.Service.Execute(worker, control.Request{Op: "ask", ID: task.ID, Body: "Approve the merge?", Key: "ask"}); err != nil {
		t.Fatal(err)
	}
	m.reply = reply(hostops.CodeMerged, "merged")
	later(c, reconcile.CheckEvery)
	pass(t, c)
	if e, _ := entry(t, c, task.ID); len(m.merges()) != 1 || e.Phase != control.QueueWaiting {
		t.Fatalf("merged while the operator's question was open; entry %+v", e)
	}
}

func TestCoordinatorRecordsAMergeWhoseResultWasLost(t *testing.T) {
	calls := 0
	c, m, task, _ := requested(t, func(hostops.MergeRequest) (hostops.Merged, error) {
		calls++
		if calls == 1 {
			return hostops.Merged{}, errors.New("host connection reset")
		}
		return hostops.Merged{Outcome: hostops.MergeDone, Code: hostops.CodeMerged, Reason: "already merged at " + reviewedHead}, nil
	})
	pass(t, c)
	if e, _ := entry(t, c, task.ID); e.Failures != 1 || len(operatorNotices(t, c, task.ID)) != 0 {
		t.Fatalf("one failure: %+v", e)
	}
	// A restarted server is a new coordinator on the same records: it keeps
	// the head's interval, then calls again.
	restarted := &Coordinator{Service: c.Service, URL: c.URL, Config: c.Config, Host: c.Host, Client: c.Client, Logf: t.Logf}
	pass(t, restarted)
	if len(m.merges()) != 1 {
		t.Fatalf("a restart called the head again within its interval: %d calls", len(m.merges()))
	}
	later(restarted, reconcile.CheckEvery)
	pass(t, restarted)
	st, _ := c.Service.Store.Snapshot()
	if st.Items[task.ID].State != "done" || len(m.merges()) != 2 {
		t.Fatalf("task %s after %d merges", st.Items[task.ID].State, len(m.merges()))
	}
}

func TestAVanishedHeadMergesWithoutItsSession(t *testing.T) {
	c, m, task, _ := requested(t, reply(hostops.CodeMerged, "merged at "+reviewedHead))
	m.reply = reply(hostops.CodePendingChecks, "running")
	pass(t, c) // the head, waiting on checks, is seen live
	m.status.Windows = nil
	pass(t, c)
	st, _ := c.Service.Store.Snapshot()
	if at := st.Attempts[st.Items[task.ID].Attempt]; at.Reason != control.EndVanished {
		t.Fatalf("attempt %+v", at)
	}
	m.reply = reply(hostops.CodeMerged, "merged at "+reviewedHead)
	later(c, reconcile.CheckEvery)
	pass(t, c)
	if st, _ = c.Service.Store.Snapshot(); st.Items[task.ID].State != "done" {
		t.Fatalf("task %s", st.Items[task.ID].State)
	}
}

func TestADeadExecutionFailsAndLeavesTheQueue(t *testing.T) {
	c, m, task, _ := requested(t, reply(hostops.CodePendingChecks, "running"))
	m.status.Windows[0].Alive = false
	st, _ := c.Service.Store.Snapshot()
	if err := c.ConfirmEnded(st.Items[task.ID].Attempt); err != nil {
		t.Fatal(err)
	}
	pass(t, c)
	st, _ = c.Service.Store.Snapshot()
	if _, queued := st.Queue[task.ID]; st.Items[task.ID].State != "failed" || queued {
		t.Fatalf("task %s, queued %v", st.Items[task.ID].State, queued)
	}
}

// mergeRequested is the plan's task, dispatched to a host whose merges
// succeed, that has requested its merge, with its attempt.
func mergeRequested(t *testing.T) (*Coordinator, *merger, control.Item, control.Attempt) {
	t.Helper()
	c, m, task, _ := requested(t, reply(hostops.CodeMerged, "merged at "+reviewedHead))
	st, _ := c.Service.Store.Snapshot()
	return c, m, task, st.Attempts[st.Items[task.ID].Attempt]
}

// The command the host op runs passes the merge guard executors still run under.
func TestTheHostsMergeCommandPassesTheMergeGuard(t *testing.T) {
	var line string
	d := hostops.Deps{Run: func(_ string, _ []string, name string, args ...string) (string, error) {
		switch {
		case strings.Contains(strings.Join(args, " "), "pr view"):
			return `{"state":"OPEN","headRefOid":"` + reviewedHead + `","baseRefName":"main","mergeable":"MERGEABLE"}`, nil
		case args[0] == "api":
			return "0\n", nil
		}
		line = remote.ShellJoin(append([]string{name}, args...))
		return "", nil
	}}
	if _, err := hostops.Merge(d, hostops.MergeRequest{Repo: "owner/demo", PR: 42, Head: reviewedHead, Method: "squash"}); err != nil {
		t.Fatal(err)
	}
	if !hook.IsMerge(line) || hook.MergeProblem(line) != "" {
		t.Fatalf("%q fails the guard: %s", line, hook.MergeProblem(line))
	}
}

// waitedLong marks the task's session as waiting since well before now.
func waitedLong(t *testing.T, c *Coordinator, m *merger, id string) control.Attempt {
	t.Helper()
	m.status.Windows[0].State = "waiting"
	var at control.Attempt
	if err := c.Service.Store.Transaction(func(st *control.State) error {
		at = st.Attempts[st.Items[id].Attempt]
		at.WaitingSince = c.Service.Now().Add(-2 * reconcile.StallWait)
		st.Attempts[at.ID] = at
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return at
}

func TestPlanTaskSessionKeepsItsWindowToReportAfterTheMerge(t *testing.T) {
	c, m, task, _ := requested(t, reply(hostops.CodeMerged, "merged at "+reviewedHead))
	c.Wake = func(reconcile.Session, string) error { return nil }
	at := waitedLong(t, c, m, task.ID)
	pass(t, c)
	st, _ := c.Service.Store.Snapshot()
	if st.Items[task.ID].State != "done" || len(m.status.Windows) != 1 || st.Attempts[at.ID].State == "ended" {
		t.Fatalf("task %s, windows %v, attempt %s: reaped as the executor was told", st.Items[task.ID].State, m.status.Windows, st.Attempts[at.ID].State)
	}
	later(c, reconcile.CleanupWait)
	pass(t, c)
	st, _ = c.Service.Store.Snapshot()
	if len(m.status.Windows) != 0 || st.Attempts[at.ID].State != "ended" {
		t.Fatalf("windows %v, attempt %s after the report window", m.status.Windows, st.Attempts[at.ID].State)
	}
}

func TestAQueuedMergeIsNotAStall(t *testing.T) {
	c, m, task, _ := requested(t, reply(hostops.CodePendingChecks, "check test is still running"))
	waitedLong(t, c, m, task.ID)
	pass(t, c)
	if n := operatorNotices(t, c, task.ID); len(n) != 0 {
		t.Fatalf("a session waiting on its merge was reported: %+v", n)
	}
}
