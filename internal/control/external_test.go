package control

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func externalPR(slug string) string { return fmt.Sprintf("https://github.com/%s/pull/%d", slug, 42) }

func TestExternalCompletionPreservesGraphAndUnblocksDependent(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	task := newTask(t, s, repo.ID, plan.ID)
	dependent := *call(t, s, op, Request{Op: "create", Item: Item{Kind: "task", Repo: repo.ID, Plan: plan.ID, Title: "Acceptance", Depends: []string{task.ID}}}).Item
	reject(t, s, op, Request{Op: "state", ID: task.ID, Version: task.Version, State: "done"}, 409)
	req := Request{Op: "complete-external", ID: task.ID, Version: task.Version, Item: Item{PR: externalPR(repo.Slug)}, Key: "external"}
	done := *call(t, s, op, req).Item
	retry := *call(t, s, op, req).Item
	st := mustSnapshot(t, s)
	if !reflect.DeepEqual(done, retry) || done.State != "done" || done.PR != req.Item.PR || done.Version != task.Version+1 || done.Attempt != "" || len(st.Attempts) != 0 {
		t.Fatalf("invalid completion: %+v, attempts %v", done, st.Attempts)
	}
	if !reflect.DeepEqual(st.Items[dependent.ID].Depends, []string{task.ID}) || !reflect.DeepEqual(st.Items[plan.ID], plan) {
		t.Fatal("completion rewrote dependency or approval")
	}
	last := st.Updates[len(st.Updates)-1]
	if last.Author != "operator" || last.Kind != "complete-external" || last.Snapshot == nil || last.Snapshot.State != "done" || !strings.Contains(last.Body, done.PR) {
		t.Fatalf("missing completion evidence: %+v", last)
	}
	req.Item.PR = externalPR("other/repo")
	reject(t, s, op, req, 409)
	approve(t, s, plan)
	_, claimed := dispatch(t, s, dependent)
	if claimed.Item.State != "running" {
		t.Fatal("dependent was not unblocked")
	}
}

func TestExternalCompletionValidationIsAtomic(t *testing.T) {
	tests := []struct {
		name, state, attemptState, role, pr string
		version, status                     int
	}{
		{"stale", "todo", "", "operator", "valid", 0, 409},
		{"missing-pr", "todo", "", "operator", "", 1, 400},
		{"other-repo", "todo", "", "operator", externalPR("other/repo"), 1, 400},
		{"dispatching", "todo", "dispatching", "operator", "valid", 1, 409},
		{"live", "failed", "running", "operator", "valid", 1, 409},
		{"running", "running", "ended", "operator", "valid", 1, 409},
		{"in-review", "in-review", "ended", "operator", "valid", 1, 409},
		{"hard-stop", "needs-human", "ended", "operator", "valid", 1, 409},
		{"done", "done", "", "operator", "valid", 1, 409},
		{"worker", "todo", "running", "worker", "valid", 1, 403},
		{"coordinator", "todo", "", "coordinator", "valid", 1, 403},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := service(t)
			repo, plan := repoPlan(t, s)
			task := newTask(t, s, repo.ID, plan.ID)
			edit(t, s.Store, func(tx *Tx) {
				task.State = tt.state
				if tt.attemptState != "" {
					task.Attempt = "a99"
					tx.PutAttempt(Attempt{ID: task.Attempt, Item: task.ID, State: tt.attemptState})
				}
				tx.PutItem(task)
			})
			before := mustSnapshot(t, s)
			pr := tt.pr
			if pr == "valid" {
				pr = externalPR(repo.Slug)
			}
			actor := Actor{Name: tt.role, Role: tt.role, Attempt: task.Attempt}
			reject(t, s, actor, Request{Op: "complete-external", ID: task.ID, Version: tt.version, Item: Item{PR: pr}}, tt.status)
			if !reflect.DeepEqual(before, mustSnapshot(t, s)) {
				t.Fatal("rejected completion changed storage")
			}
		})
	}
}

func TestExternalCompletionInactiveStatesAndQuestions(t *testing.T) {
	for _, want := range []string{"todo", "failed"} {
		t.Run(want, func(t *testing.T) {
			s := service(t)
			repo, plan := repoPlan(t, s)
			task := newTask(t, s, repo.ID, plan.ID)
			edit(t, s.Store, func(tx *Tx) {
				task.State, task.Attempt = want, "a99"
				tx.PutAttempt(Attempt{ID: task.Attempt, Item: task.ID, State: "ended"})
				tx.PutItem(task)
			})
			done := call(t, s, op, Request{Op: "complete-external", ID: task.ID, Version: task.Version, Item: Item{PR: externalPR(repo.Slug)}}).Item
			if done.State != "done" || done.Attempt != task.Attempt {
				t.Fatal("completion lost attempt history or did not finish the task")
			}
		})
	}
	t.Run("question", func(t *testing.T) {
		s := service(t)
		repo, plan := repoPlan(t, s)
		task := newTask(t, s, repo.ID, plan.ID)
		editItem(t, s.Store, task.ID, func(item *Item) { item.State = "running" })
		call(t, s, op, Request{Op: "ask", ID: task.ID, Body: "Need a decision"})
		task.Version++
		task = *call(t, s, op, Request{Op: "state", ID: task.ID, Version: task.Version, State: "failed"}).Item
		reject(t, s, op, Request{Op: "complete-external", ID: task.ID, Version: task.Version, Item: Item{PR: externalPR(repo.Slug)}}, 409)
	})
	s := service(t)
	repo, plan := repoPlan(t, s)
	for _, item := range []Item{repo, plan} {
		reject(t, s, op, Request{Op: "complete-external", ID: item.ID, Version: item.Version, Item: Item{PR: externalPR(repo.Slug)}}, 400)
	}
	reject(t, s, op, Request{Op: "complete-external", ID: "t999", Version: 1}, 404)
}

func TestExternalCompletionIgnoresItsPlansQuestion(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	task := newTask(t, s, repo.ID, plan.ID)
	call(t, s, op, Request{Op: "ask", ID: plan.ID, Body: "Need a decision"})
	done := call(t, s, op, Request{Op: "complete-external", ID: task.ID, Version: task.Version, Item: Item{PR: externalPR(repo.Slug)}}).Item
	if done.State != "done" {
		t.Fatalf("task is %s, want done", done.State)
	}
}

func TestCompletionPR(t *testing.T) {
	good := externalPR("owner/demo")
	for _, raw := range []string{"", "https://%", "http" + good[5:], good + "/", good + "?q=x", good + "#fragment", good + "/files", externalPR("other/repo"), fmt.Sprintf("https://github.com/owner/demo/pull/%d", 0), fmt.Sprintf("https://github.com/owner/demo/pull/+%d", 42), fmt.Sprintf("https://user@github.com/owner/demo/pull/%d", 42), fmt.Sprintf("https://github.com:443/owner/demo/pull/%d", 42)} {
		if completionPR(raw, "owner/demo") {
			t.Fatalf("accepted %q", raw)
		}
	}
	if !completionPR(good, "owner/demo") {
		t.Fatal("valid PR rejected")
	}
}
