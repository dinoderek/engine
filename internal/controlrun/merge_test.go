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
	"github.com/dinoderek/engine/internal/remote"
	"github.com/dinoderek/engine/internal/run"
)

const reviewedHead = "0123456789abcdef0123456789abcdef01234567"

var reviewedPR = fmt.Sprintf("https://github.com/owner/demo/pull/%d", 42)

// merger is a host whose merge op answers with reply, recording each request.
type merger struct {
	*host
	reply    func() (hostops.Merged, error)
	requests []hostops.MergeRequest
}

func (m *merger) ControlMerge(r hostops.MergeRequest) (hostops.Merged, error) {
	m.requests = append(m.requests, r)
	return m.reply()
}

// requested dispatches the task, then has its executor move it to in-review,
// take the merge lock when lock is set, and request the merge.
func requested(t *testing.T, lock bool, reply func() (hostops.Merged, error)) (*Coordinator, *merger, control.Item, control.Actor) {
	t.Helper()
	c, h, _, _, task := setup(t)
	m := &merger{host: h, reply: reply}
	c.Client = func(hosts.Host) remote.Client { return m }
	pass(t, c)
	pass(t, c)
	st, _ := c.Service.Store.Snapshot()
	worker, err := c.Service.Authenticate(m.launches[st.Attempts[st.Items[task.ID].Attempt].RunID].Credential, "operator")
	if err != nil {
		t.Fatal(err)
	}
	do := func(r control.Request) control.Response {
		t.Helper()
		r.Key = control.Secret()
		out, err := c.Service.Execute(worker, r)
		if err != nil {
			t.Fatal(r.Op, err)
		}
		return out
	}
	do(control.Request{Op: "state", ID: task.ID, Version: st.Items[task.ID].Version, State: "in-review", Item: control.Item{PR: reviewedPR}})
	token := ""
	if lock {
		token = do(control.Request{Op: "lock-acquire", Attempt: worker.Attempt}).Lock.Token
	} else if err := c.Service.Store.Transaction(func(s *control.State) error {
		s.Locks[task.Repo] = control.Lock{Repo: task.Repo, Attempt: worker.Attempt, Token: "t"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	do(control.Request{Op: "merge-request", ID: task.ID, Token: map[bool]string{true: token, false: "t"}[lock], Merge: &control.MergeRequest{
		PR: reviewedPR, Head: reviewedHead, Method: "squash", Verdict: "APPROVED"}})
	if !lock { // The lock moves away after the request.
		if err := c.Service.Store.Transaction(func(s *control.State) error { delete(s.Locks, task.Repo); return nil }); err != nil {
			t.Fatal(err)
		}
	}
	return c, m, task, worker
}

func reply(outcome, reason string) func() (hostops.Merged, error) {
	return func() (hostops.Merged, error) { return hostops.Merged{Outcome: outcome, Reason: reason}, nil }
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

func TestCoordinatorMergesOnTheTasksHostAndCompletesTheTask(t *testing.T) {
	c, m, task, _ := requested(t, true, reply(hostops.MergeDone, "merged at "+reviewedHead))
	var woken []string
	c.Wake = func(_ run.Session, text string) error { woken = append(woken, text); return nil }
	m.status.Windows[0].State = "waiting"
	pass(t, c)
	if len(m.requests) != 1 || m.requests[0] != (hostops.MergeRequest{Repo: "owner/demo", PR: 42, Head: reviewedHead, Method: "squash"}) {
		t.Fatalf("merge requests %+v", m.requests)
	}
	st, _ := c.Service.Store.Snapshot()
	if got := st.Items[task.ID]; got.State != "done" || got.PR != reviewedPR {
		t.Fatalf("task %+v", got)
	}
	if _, held := st.Locks[task.Repo]; held {
		t.Fatal("lock kept after the merge")
	}
	if len(woken) == 0 {
		t.Fatal("the executor was not told about its merge")
	}
	pass(t, c)
	if len(m.requests) != 1 {
		t.Fatal("an answered request merged again")
	}
}

func TestCoordinatorHandsBackAMergeItCannotMake(t *testing.T) {
	for name, reason := range map[string]string{
		"main moved":   "main moved: the head is 2 commits behind it; merge it in and gate again",
		"red checks":   "check test is not green",
		"head changed": "the PR's head is fff, not the reviewed " + reviewedHead,
	} {
		t.Run(name, func(t *testing.T) {
			c, _, task, _ := requested(t, true, reply(hostops.MergeRefused, reason))
			pass(t, c)
			u, ok := lastUpdate(t, c, task.ID, control.MergeRefused)
			st, _ := c.Service.Store.Snapshot()
			if !ok || !strings.Contains(u.Body, reason) || st.Items[task.ID].State != "in-review" || st.Locks[task.Repo].Attempt == "" {
				t.Fatalf("refusal %+v, task %s, lock %+v", u, st.Items[task.ID].State, st.Locks[task.Repo])
			}
		})
	}
}

func TestCoordinatorRefusesAMergeWithoutTheLock(t *testing.T) {
	c, m, task, _ := requested(t, false, reply(hostops.MergeDone, "merged"))
	pass(t, c)
	if u, ok := lastUpdate(t, c, task.ID, control.MergeRefused); !ok || !strings.Contains(u.Body, "merge lock") || len(m.requests) != 0 {
		t.Fatalf("refusal %+v, merges %v", u, m.requests)
	}
}

func TestCoordinatorWaitsForPendingChecksAndForTheOperator(t *testing.T) {
	c, m, task, worker := requested(t, true, reply(hostops.MergePending, "check test is still running"))
	pass(t, c)
	if _, ok := lastUpdate(t, c, task.ID, control.MergeMerged, control.MergeRefused); ok || len(m.requests) != 1 {
		t.Fatal("a pending merge was answered")
	}
	if _, err := c.Service.Execute(worker, control.Request{Op: "ask", ID: task.ID, Body: "Approve the merge?", Key: "ask"}); err != nil {
		t.Fatal(err)
	}
	m.reply = reply(hostops.MergeDone, "merged")
	pass(t, c)
	if len(m.requests) != 1 {
		t.Fatal("merged while the operator's question was open")
	}
}

// failureNotices are the coordinator's merge-failure messages to the operator.
func failureNotices(t *testing.T, c *Coordinator, id string) []control.Update {
	t.Helper()
	st, _ := c.Service.Store.Snapshot()
	var out []control.Update
	for _, u := range st.Updates {
		if u.Item == id && u.Recipient == "operator" && strings.Contains(u.Body, "gh auth status") {
			out = append(out, u)
		}
	}
	return out
}

func acknowledge(t *testing.T, c *Coordinator, u control.Update) {
	t.Helper()
	if _, err := c.Service.Execute(control.Actor{Name: "operator", Role: "operator"}, control.Request{Op: "ack", ReplyTo: u.ID, Key: control.Secret()}); err != nil {
		t.Fatal(err)
	}
}

func TestCoordinatorRecordsAMergeWhoseResultWasLost(t *testing.T) {
	calls := 0
	c, m, task, _ := requested(t, true, func() (hostops.Merged, error) {
		calls++
		if calls == 1 {
			return hostops.Merged{}, errors.New("host connection reset")
		}
		return hostops.Merged{Outcome: hostops.MergeDone, Reason: "already merged at " + reviewedHead}, nil
	})
	pass(t, c)
	acknowledge(t, c, failureNotices(t, c, task.ID)[0])
	pass(t, c)
	st, _ := c.Service.Store.Snapshot()
	if st.Items[task.ID].State != "done" || len(m.requests) != 2 {
		t.Fatalf("task %s after %d merges", st.Items[task.ID].State, len(m.requests))
	}
}

func TestCoordinatorHoldsAFailedMergeUntilTheOperatorAcknowledges(t *testing.T) {
	calls := 0
	c, _, task, _ := requested(t, true, func() (hostops.Merged, error) {
		calls++
		return hostops.Merged{}, errors.New("gh: authentication required")
	})
	pass(t, c)
	pass(t, c)
	notices := failureNotices(t, c, task.ID)
	st, _ := c.Service.Store.Snapshot()
	if len(notices) != 1 || calls != 1 || st.Items[task.ID].State != "in-review" {
		t.Fatalf("%d notices after %d attempts; task %s", len(notices), calls, st.Items[task.ID].State)
	}
	acknowledge(t, c, notices[0])
	pass(t, c)
	if notices = failureNotices(t, c, task.ID); len(notices) != 2 || calls != 2 {
		t.Fatalf("%d notices after %d attempts once acknowledged", len(notices), calls)
	}
}

func TestStandaloneSessionKeepsItsWindowToReportAfterTheMerge(t *testing.T) {
	c, h, repo, _, _ := setup(t)
	if err := c.Service.Store.Transaction(func(st *control.State) error {
		for id, item := range st.Items {
			if item.Kind == "plan" || item.Kind == "task" {
				delete(st.Items, id)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	task := execute(t, c.Service, control.Request{Op: "create", Item: control.Item{Kind: "task", Title: "Alone", Repo: repo.ID, Body: "## Outcome\nWorks"}})
	task = execute(t, c.Service, control.Request{Op: "approve", ID: task.ID, Version: task.Version})
	execute(t, c.Service, control.Request{Op: "run", ID: task.ID, Version: task.Version})
	m := &merger{host: h, reply: reply(hostops.MergeDone, "merged at "+reviewedHead)}
	c.Client = func(hosts.Host) remote.Client { return m }
	pass(t, c)
	st, _ := c.Service.Store.Snapshot()
	at := st.Attempts[st.Items[task.ID].Attempt]
	worker, err := c.Service.Authenticate(m.launches[at.RunID].Credential, "operator")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []control.Request{
		{Op: "state", ID: task.ID, Version: st.Items[task.ID].Version, State: "in-review", Item: control.Item{PR: reviewedPR}},
		{Op: "lock-acquire", Attempt: at.ID},
	} {
		r.Key = control.Secret()
		if _, err := c.Service.Execute(worker, r); err != nil {
			t.Fatal(r.Op, err)
		}
	}
	st, _ = c.Service.Store.Snapshot()
	if _, err := c.Service.Execute(worker, control.Request{Op: "merge-request", ID: task.ID, Token: st.Locks[repo.ID].Token, Key: "ready", Merge: &control.MergeRequest{
		PR: reviewedPR, Head: reviewedHead, Method: "squash", Verdict: "APPROVED"}}); err != nil {
		t.Fatal(err)
	}
	m.status.Windows[0].State = "waiting"
	c.Wake = func(run.Session, string) error { return nil }
	pass(t, c)
	st, _ = c.Service.Store.Snapshot()
	if st.Items[task.ID].State != "done" || len(m.status.Windows) != 1 || st.Attempts[at.ID].State == "ended" {
		t.Fatalf("task %s, windows %v, attempt %s: the window closed as the executor was told", st.Items[task.ID].State, m.status.Windows, st.Attempts[at.ID].State)
	}
	later := c.Service.Now().Add(reportWindow)
	c.Service.Now = func() time.Time { return later }
	pass(t, c)
	st, _ = c.Service.Store.Snapshot()
	if len(m.status.Windows) != 0 || st.Attempts[at.ID].State != "ended" {
		t.Fatalf("windows %v, attempt %s after the report window", m.status.Windows, st.Attempts[at.ID].State)
	}
}

func TestCoordinatorLeavesAnUnreachableHostsMergePending(t *testing.T) {
	c, _, task, _ := requested(t, true, func() (hostops.Merged, error) {
		return hostops.Merged{}, &remote.UnreachableError{Host: "devbox", Address: "devbox", Detail: "timeout"}
	})
	pass(t, c)
	st, _ := c.Service.Store.Snapshot()
	if _, ok := control.PendingMerge(&st, task.ID); !ok {
		t.Fatal("an outage answered the request")
	}
	for _, u := range st.Updates {
		if u.Item == task.ID && u.Recipient == "operator" {
			t.Fatalf("an outage told the operator: %+v", u)
		}
	}
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
		at.WaitingSince = c.Service.Now().Add(-2 * run.StallWait)
		st.Attempts[at.ID] = at
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return at
}

func TestPlanTaskSessionKeepsItsWindowToReportAfterTheMerge(t *testing.T) {
	c, m, task, _ := requested(t, true, reply(hostops.MergeDone, "merged at "+reviewedHead))
	c.Wake = func(run.Session, string) error { return nil }
	at := waitedLong(t, c, m, task.ID)
	pass(t, c)
	st, _ := c.Service.Store.Snapshot()
	if st.Items[task.ID].State != "done" || len(m.status.Windows) != 1 || st.Attempts[at.ID].State == "ended" {
		t.Fatalf("task %s, windows %v, attempt %s: reaped as the executor was told", st.Items[task.ID].State, m.status.Windows, st.Attempts[at.ID].State)
	}
	later := c.Service.Now().Add(reportWindow)
	c.Service.Now = func() time.Time { return later }
	pass(t, c)
	st, _ = c.Service.Store.Snapshot()
	if len(m.status.Windows) != 0 || st.Attempts[at.ID].State != "ended" {
		t.Fatalf("windows %v, attempt %s after the report window", m.status.Windows, st.Attempts[at.ID].State)
	}
}

func TestAPendingMergeIsNotAStall(t *testing.T) {
	c, m, task, _ := requested(t, true, reply(hostops.MergePending, "check test is still running"))
	waitedLong(t, c, m, task.ID)
	pass(t, c)
	st, _ := c.Service.Store.Snapshot()
	for _, u := range st.Updates {
		if u.Item == task.ID && u.Recipient == "operator" {
			t.Fatalf("a session waiting on its merge was reported: %+v", u)
		}
	}
}
