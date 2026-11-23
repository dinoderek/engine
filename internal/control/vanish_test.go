package control

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// prior is an ended earlier attempt of task t2, for TestTheVanishRule.
type prior struct {
	reason           string
	crashed, limited bool
	early, unopened  bool
	hostLost         bool
	upgraded         bool
}

func TestTheVanishRule(t *testing.T) {
	start := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	live := start.Add(time.Hour)
	crash := prior{reason: EndVanished, crashed: true}
	claimedAt := Attempt{Started: start, LiveAt: live}
	cases := []struct {
		name          string
		task, plan    Item
		at            Attempt
		before        []prior
		question      bool
		reason, state string
		crashed       bool
	}{
		{name: "a claimed death with no PR resumes", at: claimedAt, reason: EndVanished, state: "running", crashed: true},
		{name: "a claimed death with a PR resumes", task: Item{PR: "pr-url"}, at: claimedAt, reason: EndVanished, state: "running", crashed: true},
		{name: "a claimed death with an open question resumes", question: true, at: claimedAt, reason: EndVanished, state: "running", crashed: true},
		{name: "the resume of a vanished attempt resumes again", task: Item{PR: "pr-url"}, at: claimedAt, before: []prior{crash}, reason: EndVanished, state: "running", crashed: true},
		{name: "the cap's death fails the task", at: claimedAt, before: []prior{crash, crash}, reason: EndFailed, state: "failed", crashed: true},
		{name: "a release after done resets the count", at: claimedAt, before: []prior{crash, {}, crash}, reason: EndVanished, state: "running", crashed: true},
		{name: "the operator's stop resets the count", at: claimedAt, before: []prior{crash, {reason: EndStopped}, crash}, reason: EndVanished, state: "running", crashed: true},
		{name: "a retry after the cap starts the count again", at: claimedAt, before: []prior{crash, crash, {reason: EndFailed, crashed: true}, crash}, reason: EndVanished, state: "running", crashed: true},
		{name: "an early death ends the run", at: claimedAt, before: []prior{crash, {reason: EndEarlyDeath, early: true}, crash}, reason: EndVanished, state: "running", crashed: true},
		{name: "limit, stopped and unopened attempts neither count nor reset", at: claimedAt,
			before: []prior{crash, {reason: EndVanished, limited: true}, {reason: EndVanished}, {reason: EndVanished, unopened: true}, crash}, reason: EndFailed, state: "failed", crashed: true},
		{name: "the operator's move neither counts nor resets", at: claimedAt, before: []prior{crash, {reason: EndMoved}, crash}, reason: EndFailed, state: "failed", crashed: true},
		{name: "a death at a limit is uncounted", at: Attempt{Started: start, LiveAt: live, Limited: true}, before: []prior{crash, crash}, reason: EndVanished, state: "running"},
		{name: "coordination stopped after it was last seen live", plan: Item{StoppedAt: live}, at: claimedAt, before: []prior{crash, crash}, reason: EndVanished, state: "running"},
		{name: "stopped before it was ever seen live", plan: Item{StoppedAt: start.Add(time.Minute)}, at: Attempt{Started: start}, reason: EndVanished, state: "running"},
		{name: "stopped, then seen live after the restart", plan: Item{StoppedAt: live.Add(-time.Minute)}, at: claimedAt, reason: EndVanished, state: "running", crashed: true},
		{name: "an unclaimed death is early, PR or not", task: Item{PR: "pr-url"}, at: Attempt{Started: start, LiveAt: start.Add(time.Minute)}, reason: EndEarlyDeath, state: "todo"},
		{name: "a second unclaimed death fails", at: Attempt{Started: start}, before: []prior{{reason: EndEarlyDeath, early: true}}, reason: EndFailed, state: "failed"},
		{name: "a claimed death in a host-wide loss resumes, uncounted", at: Attempt{Started: start, LiveAt: live, HostLost: true}, before: []prior{crash, crash}, reason: EndVanished, state: "running"},
		{name: "an unclaimed death in a host-wide loss is an uncounted early death", at: Attempt{Started: start, HostLost: true}, before: []prior{{reason: EndEarlyDeath, early: true}}, reason: EndEarlyDeath, state: "todo"},
		{name: "host-wide losses neither count nor reset the cap", at: claimedAt,
			before: []prior{crash, {reason: EndVanished, hostLost: true}, {reason: EndEarlyDeath, hostLost: true}, crash}, reason: EndFailed, state: "failed", crashed: true},
		{name: "an earlier host-wide early death is no strike", at: Attempt{Started: start}, before: []prior{{reason: EndEarlyDeath, hostLost: true}}, reason: EndEarlyDeath, state: "todo"},
		{name: "an unclaimed death after crashes is still the first", at: Attempt{Started: start}, before: []prior{crash, crash}, reason: EndEarlyDeath, state: "todo"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := vanishState(c.task, c.plan, c.before)
			c.at.ID, c.at.Item, c.at.State, c.at.Window = "a99", "t2", "running", "@1"
			st.Attempts[c.at.ID] = c.at
			if c.question {
				st.Updates = append(st.Updates, Update{ID: 1, Item: "t2", Kind: "ask", RequiresReply: true})
			}
			s := Service{Now: func() time.Time { return live.Add(time.Hour) }}
			tx := st.load(t)
			at := s.endDead(tx, Actor{Name: "coordinator", Role: "coordinator"}, c.at, "gone")
			if at.Reason != c.reason || at.Crashed != c.crashed || at.Early && c.at.HostLost || tx.item("t2").State != c.state {
				t.Fatalf("ended %s crashed %v, task %s; want %s crashed %v, task %s", at.Reason, at.Crashed, tx.item("t2").State, c.reason, c.crashed, c.state)
			}
		})
	}
}

// vanishState holds plan p1 and its running task t2, whose current attempt
// is a99, after the earlier attempts before, oldest first.
func vanishState(task, plan Item, before []prior) records {
	plan.ID, plan.Kind = "p1", "plan"
	task.ID, task.Kind, task.Plan, task.State, task.Attempt = "t2", "task", "p1", "running", "a99"
	st := newRecords()
	st.Items[plan.ID], st.Items[task.ID] = plan, task
	for i, p := range before {
		id := fmt.Sprintf("a%d", i+1)
		window := "@1"
		if p.unopened {
			window = ""
		}
		st.Attempts[id] = Attempt{ID: id, Item: "t2", State: "ended", Window: window, Reason: p.reason,
			Crashed: p.crashed, Limited: p.limited, Early: p.early, HostLost: p.hostLost, Upgraded: p.upgraded}
	}
	st.Attempts["a50"] = Attempt{ID: "a50", Item: "t8", State: "ended", Window: "@1", Reason: EndFailed}
	return st
}

func TestReconcileResumesClaimedDeathsUntilTheCap(t *testing.T) {
	s := service(t)
	ended := 0
	s.Reconcile = func(string) error { ended++; return nil }
	repo, plan := repoPlan(t, s)
	task := newTask(t, s, repo.ID, plan.ID)
	approve(t, s, plan)
	a, out := dispatch(t, s, task)
	launched(t, s, out.Attempt.ID)
	question := call(t, s, a, Request{Op: "ask", ID: task.ID, Body: "Which API?"}).Updates[0]
	seenLive(t, s, out.Attempt.ID)
	at := call(t, s, op, Request{Op: "attempt-reconcile", ID: out.Attempt.ID, Body: "host rebooted"}).Attempt
	current := call(t, s, op, Request{Op: "get", ID: task.ID}).Item
	if at.Reason != EndVanished || at.State != "ended" || current.State != "needs-human" {
		t.Fatalf("a vanished attempt with an open question failed its task: %+v %+v", at, current)
	}
	again := call(t, s, op, Request{Op: "attempt-reconcile", ID: out.Attempt.ID, Body: "again"}).Attempt
	if again.Reason != EndVanished || ended != 2 {
		t.Fatalf("reconciling twice changed the ended attempt: %+v", again)
	}
	reject(t, s, op, Request{Op: "claim", ID: task.ID, Body: "devbox", State: "claude"}, 409)
	call(t, s, op, Request{Op: "resolve", ReplyTo: question.ID, Body: "The second one."})
	worker, resumed := dispatch(t, s, *current)
	if resumed.Item.State != "running" || resumed.Attempt.ID == out.Attempt.ID {
		t.Fatalf("an answered, vanished task was not dispatched again: %+v", resumed.Item)
	}
	launched(t, s, resumed.Attempt.ID)
	seenLive(t, s, resumed.Attempt.ID)
	call(t, s, worker, Request{Op: "say", ID: task.ID, Body: "started"})
	again = call(t, s, op, Request{Op: "attempt-reconcile", ID: resumed.Attempt.ID, Body: "crashed"}).Attempt
	current = call(t, s, op, Request{Op: "get", ID: task.ID}).Item
	if again.Reason != EndVanished || current.State != "running" {
		t.Fatalf("the resume's claimed death did not resume: %+v %+v", again, current)
	}
	worker, resumed = dispatch(t, s, *current)
	launched(t, s, resumed.Attempt.ID)
	call(t, s, worker, Request{Op: "say", ID: task.ID, Body: "started again"})
	failed := call(t, s, op, Request{Op: "attempt-reconcile", ID: resumed.Attempt.ID, Body: "crashed again"}).Attempt
	current = call(t, s, op, Request{Op: "get", ID: task.ID}).Item
	if failed.Reason != EndFailed || current.State != "failed" {
		t.Fatalf("the cap's claimed death did not fail its task: %+v %+v", failed, current)
	}
	reject(t, s, op, Request{Op: "claim", ID: task.ID, Body: "devbox", State: "claude"}, 409)
}

func TestStopRecordsWhenCoordinationStopped(t *testing.T) {
	s := service(t)
	_, plan := repoPlan(t, s)
	plan = approve(t, s, plan)
	plan = *call(t, s, op, Request{Op: "stop", ID: plan.ID}).Item
	if !plan.StoppedAt.IsZero() {
		t.Fatal("stopping idle coordination recorded a stop")
	}
	call(t, s, op, Request{Op: "run", ID: plan.ID})
	plan = *call(t, s, op, Request{Op: "stop", ID: plan.ID}).Item
	if !plan.StoppedAt.Equal(s.now()) || plan.Coordinating {
		t.Fatalf("stop did not record its time: %+v", plan)
	}
}

func TestEndDeadIsIdempotentAndRefusesAnUnknownAttempt(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	task := newTask(t, s, repo.ID, plan.ID)
	approve(t, s, plan)
	_, out := dispatch(t, s, task)
	if _, err := s.EndDead("absent", "gone", ""); err == nil {
		t.Fatal("ended an unknown attempt")
	}
	first, err := s.EndDead(out.Attempt.ID, "gone", "")
	if err != nil || first.Reason != EndEarlyDeath {
		t.Fatalf("%v %+v", err, first)
	}
	second, err := s.EndDead(out.Attempt.ID, "gone", "")
	if err != nil || !second.Ended.Equal(first.Ended) || second.Reason != EndEarlyDeath {
		t.Fatalf("ending twice changed the attempt: %v %+v", err, second)
	}
}

// launched records that attempt id's window opened, as a coordinator does
// once its host starts the session.
func launched(t *testing.T, s Service, id string) {
	t.Helper()
	if err := s.SetAttempt(id, "running", "@1"); err != nil {
		t.Fatal(err)
	}
}

// seenLive records that a coordinator pass saw attempt id's window live.
func seenLive(t *testing.T, s Service, id string) {
	t.Helper()
	editAttempt(t, s.Store, id, func(at *Attempt) { at.LiveAt = s.now() })
}

// The operator's reconcile of a dispatch that never launched, which its host
// confirmed (Service.Reconcile), ends the attempt vanished and returns the
// task to todo for a new dispatch, rather than failing it.
func TestReconcileOfAnUnlaunchedDispatchReturnsTheTaskToTodo(t *testing.T) {
	s := service(t)
	s.Reconcile = func(string) error { return nil }
	repo, plan := repoPlan(t, s)
	task := newTask(t, s, repo.ID, plan.ID)
	approve(t, s, plan)
	_, out := dispatch(t, s, task)
	at := call(t, s, op, Request{Op: "attempt-reconcile", ID: out.Attempt.ID, Body: "spawn died before its window"}).Attempt
	current := call(t, s, op, Request{Op: "get", ID: task.ID}).Item
	if at.State != "ended" || at.Reason != EndVanished || current.State != "todo" {
		t.Fatalf("an unlaunched dispatch: %+v, task %s", at, current.State)
	}
	if _, again := dispatch(t, s, *current); again.Attempt.ID == out.Attempt.ID || again.Item.State != "running" {
		t.Fatalf("the task was not dispatched again: %+v", again)
	}
}

// A refusal of the host check reaches the operator as its own problem, not
// as a storage failure, and ends nothing.
func TestReconcileRefusalIsTheChecksProblem(t *testing.T) {
	s := service(t)
	s.Reconcile = func(string) error {
		return &Problem{Status: 409, Reason: ReasonReconcileRefused, Message: "execution is still alive"}
	}
	repo, plan := repoPlan(t, s)
	task := newTask(t, s, repo.ID, plan.ID)
	approve(t, s, plan)
	_, out := dispatch(t, s, task)
	_, err := s.Execute(op, Request{Op: "attempt-reconcile", ID: out.Attempt.ID, Body: "gone", Key: "k"})
	var p *Problem
	if !errors.As(err, &p) || p.Status != 409 || p.Reason != ReasonReconcileRefused || err.Error() != "reconcile-refused: execution is still alive" {
		t.Fatalf("err = %v", err)
	}
	if at := mustSnapshot(t, s).Attempts[out.Attempt.ID]; at.State != "dispatching" {
		t.Fatalf("a refused reconcile changed the attempt: %+v", at)
	}
}

// A dispatch launched between the host check and the transaction was never
// checked as running, so the reconcile is refused and the task kept.
func TestReconcileRefusesAnAttemptThatMovedDuringTheCheck(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	task := newTask(t, s, repo.ID, plan.ID)
	approve(t, s, plan)
	_, out := dispatch(t, s, task)
	s.Reconcile = func(id string) error { return s.SetAttempt(id, "running", "@1") }
	_, err := s.Execute(op, Request{Op: "attempt-reconcile", ID: out.Attempt.ID, Body: "gone", Key: "k"})
	var p *Problem
	if !errors.As(err, &p) || p.Status != 409 || p.Reason != ReasonReconcileRefused || !strings.Contains(err.Error(), "moved from dispatching to running") {
		t.Fatalf("err = %v", err)
	}
	st := mustSnapshot(t, s)
	if at := st.Attempts[out.Attempt.ID]; at.State != "running" || st.Items[task.ID].State != "running" {
		t.Fatalf("a reconcile of a moved attempt changed it: %+v, task %s", at, st.Items[task.ID].State)
	}
}
