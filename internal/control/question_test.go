package control

import "testing"

func TestTaskAsksOnItsPlanAndTheResolutionReleasesEveryTask(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	other := *call(t, s, op, Request{Op: "create", Item: Item{Kind: "plan", Repo: repo.ID, Title: "Another", Body: "Other"}}).Item
	one := newTask(t, s, repo.ID, plan.ID)
	two := newTask(t, s, repo.ID, plan.ID)
	approve(t, s, plan)
	first, _ := dispatch(t, s, one)
	second, claimed := dispatch(t, s, two)
	reject(t, s, first, Request{Op: "ask", ID: other.ID, Body: "Is main red?"}, 403)
	reject(t, s, first, Request{Op: "ask", ID: two.ID, Body: "Is main red?"}, 403)
	question := call(t, s, first, Request{Op: "ask", ID: plan.ID, Body: "The unit lane is red on main"}).Updates[0]
	if question.Item != plan.ID || question.Task != one.ID || !question.RequiresReply || question.Recipient != "operator" {
		t.Fatalf("question = %+v, want it on the plan from %s", question, one.ID)
	}
	st := mustSnapshot(t, s)
	if st.Items[one.ID].State != "needs-human" || st.Items[two.ID].State != "running" {
		t.Fatalf("states = %s, %s", st.Items[one.ID].State, st.Items[two.ID].State)
	}
	if PendingQuestion(&st, two.ID) != question.ID {
		t.Fatal("the plan question does not block the sibling")
	}
	reject(t, s, second, Request{Op: "state", ID: two.ID, Version: claimed.Item.Version, State: "in-review"}, 409)
	seen := call(t, s, second, Request{Op: "updates", ID: plan.ID}).Updates
	if len(seen) == 0 || seen[len(seen)-1].ID != question.ID {
		t.Fatalf("sibling reads %+v, want the plan question", seen)
	}
	reject(t, s, op, Request{Op: "resolve", ReplyTo: question.ID, Body: "Fixed on main", Item: Item{Plan: plan.ID}}, 400)
	out := call(t, s, op, Request{Op: "resolve", ReplyTo: question.ID, Body: "Fixed on main; merge it in"})
	if len(out.Updates) != 2 || out.Updates[0].Item != plan.ID || out.Updates[1].Item != one.ID || out.Updates[1].Kind != "state" {
		t.Fatalf("resolution = %+v, want the plan resolution and the asker's restored state", out.Updates)
	}
	st = mustSnapshot(t, s)
	if st.Items[one.ID].State != "running" || PendingQuestion(&st, one.ID) != 0 || PendingQuestion(&st, two.ID) != 0 {
		t.Fatalf("after resolution: %s, pending %d and %d", st.Items[one.ID].State, PendingQuestion(&st, one.ID), PendingQuestion(&st, two.ID))
	}
}

func TestResolveRestoresTheStateBeforeTheHardStop(t *testing.T) {
	for _, want := range []string{"running", "in-review"} {
		t.Run(want, func(t *testing.T) {
			s := service(t)
			repo, plan := repoPlan(t, s)
			task := newTask(t, s, repo.ID, plan.ID)
			approve(t, s, plan)
			worker, claimed := dispatch(t, s, task)
			if want == "in-review" {
				call(t, s, worker, Request{Op: "state", ID: task.ID, Version: claimed.Item.Version, State: want, Item: Item{PR: good().PR}})
			}
			question := call(t, s, worker, Request{Op: "ask", ID: task.ID, Body: "Approve the schema change?"}).Updates[0]
			call(t, s, op, Request{Op: "say", ID: task.ID, Body: "Looking"})
			call(t, s, op, Request{Op: "resolve", ReplyTo: question.ID, Body: "Approved"})
			if got := mustSnapshot(t, s).Items[task.ID].State; got != want {
				t.Fatalf("state = %s, want %s", got, want)
			}
		})
	}
}

func TestTaskStaysHeldUntilItsLastQuestionIsResolved(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	task := newTask(t, s, repo.ID, plan.ID)
	approve(t, s, plan)
	worker, _ := dispatch(t, s, task)
	questions := []Update{
		call(t, s, worker, Request{Op: "ask", ID: task.ID, Body: "Approve the schema change?"}).Updates[0],
		call(t, s, worker, Request{Op: "ask", ID: task.ID, Body: "Approve the new endpoint?"}).Updates[0],
		call(t, s, worker, Request{Op: "ask", ID: plan.ID, Body: "The lint lane is red on main"}).Updates[0],
	}
	for i, q := range questions {
		call(t, s, op, Request{Op: "resolve", ReplyTo: q.ID, Body: "Yes"})
		want := "needs-human"
		if i == len(questions)-1 {
			want = "running"
		}
		if got := mustSnapshot(t, s).Items[task.ID].State; got != want {
			t.Fatalf("after resolving %d of %d: state = %s, want %s", i+1, len(questions), got, want)
		}
	}
}

func TestResolveLeavesAMovedOrEndedTaskAlone(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	moved := newTask(t, s, repo.ID, plan.ID)
	ended := newTask(t, s, repo.ID, plan.ID)
	approve(t, s, plan)
	first, _ := dispatch(t, s, moved)
	second, claimed := dispatch(t, s, ended)
	q1 := call(t, s, first, Request{Op: "ask", ID: moved.ID, Body: "Approve?"}).Updates[0]
	q2 := call(t, s, second, Request{Op: "ask", ID: ended.ID, Body: "Approve?"}).Updates[0]
	held := mustSnapshot(t, s).Items[moved.ID]
	call(t, s, op, Request{Op: "state", ID: moved.ID, Version: held.Version, State: "failed"})
	if err := s.SetAttempt(claimed.Attempt.ID, "ended", ""); err != nil {
		t.Fatal(err)
	}
	call(t, s, op, Request{Op: "resolve", ReplyTo: q1.ID, Body: "Yes"})
	call(t, s, op, Request{Op: "resolve", ReplyTo: q2.ID, Body: "Yes"})
	st := mustSnapshot(t, s)
	if st.Items[moved.ID].State != "failed" || st.Items[ended.ID].State != "needs-human" {
		t.Fatalf("states = %s, %s; want failed and needs-human", st.Items[moved.ID].State, st.Items[ended.ID].State)
	}
}

func TestOperatorStateChangeStillEndsAHardStop(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	task := newTask(t, s, repo.ID, plan.ID)
	approve(t, s, plan)
	worker, claimed := dispatch(t, s, task)
	held := call(t, s, worker, Request{Op: "state", ID: task.ID, Version: claimed.Item.Version, State: "needs-human"}).Item
	reject(t, s, worker, Request{Op: "state", ID: task.ID, Version: held.Version, State: "running"}, 403)
	if got := call(t, s, op, Request{Op: "state", ID: task.ID, Version: held.Version, State: "running"}).Item.State; got != "running" {
		t.Fatalf("state = %s, want running", got)
	}
}

func TestHeldFromIsTheNewestStateBeforeNeedsHuman(t *testing.T) {
	snap := func(state string) *Item { return &Item{ID: "t1", State: state} }
	cases := []struct {
		name    string
		updates []Update
		want    string
	}{
		{"none", nil, ""},
		{"in review", []Update{{Item: "t1", Snapshot: snap("running")}, {Item: "t1", Snapshot: snap("in-review")}, {Item: "t1", Snapshot: snap("needs-human")}}, "in-review"},
		{"other item", []Update{{Item: "t1", Snapshot: snap("running")}, {Item: "p1", Snapshot: &Item{ID: "p1", State: "executing"}}}, "running"},
		{"not active", []Update{{Item: "t1", Snapshot: snap("todo")}, {Item: "t1", Snapshot: snap("needs-human")}}, ""},
	}
	for _, tc := range cases {
		if got := heldFrom(&State{Updates: tc.updates}, "t1"); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
