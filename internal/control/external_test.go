package control

import (
	"fmt"
	"reflect"
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
	if last.Author != "operator" || last.Kind != "complete-external" || last.Snapshot == nil || last.Snapshot.PR != done.PR {
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
			if err := s.Store.Transaction(func(st *State) error {
				task.State = tt.state
				if tt.attemptState != "" {
					task.Attempt = "a99"
					st.Attempts[task.Attempt] = Attempt{ID: task.Attempt, Item: task.ID, State: tt.attemptState}
				}
				st.Items[task.ID] = task
				return nil
			}); err != nil {
				t.Fatal(err)
			}
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
	for _, state := range []string{"prepare", "approved", "todo", "failed"} {
		t.Run(state, func(t *testing.T) {
			s := service(t)
			repo, _ := repoPlan(t, s)
			task := newTask(t, s, repo.ID, "")
			if err := s.Store.Transaction(func(st *State) error {
				task.State, task.Coordinating, task.Attempt = state, true, "a99"
				st.Attempts[task.Attempt] = Attempt{ID: task.Attempt, Item: task.ID, State: "ended"}
				st.Items[task.ID] = task
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			done := call(t, s, op, Request{Op: "complete-external", ID: task.ID, Version: task.Version, Item: Item{PR: externalPR(repo.Slug)}}).Item
			if done.Coordinating || done.Attempt != task.Attempt {
				t.Fatal("completion lost attempt history or retained coordination")
			}
		})
	}
	for _, parentQuestion := range []bool{false, true} {
		t.Run(fmt.Sprint("question-", parentQuestion), func(t *testing.T) {
			s := service(t)
			repo, plan := repoPlan(t, s)
			task := newTask(t, s, repo.ID, plan.ID)
			target := task.ID
			if parentQuestion {
				target = plan.ID
			}
			if !parentQuestion {
				if err := s.Store.Transaction(func(st *State) error {
					item := st.Items[task.ID]
					item.State = "running"
					st.Items[item.ID] = item
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			call(t, s, op, Request{Op: "ask", ID: target, Body: "Need a decision"})
			if !parentQuestion {
				task.Version++
				task = *call(t, s, op, Request{Op: "state", ID: task.ID, Version: task.Version, State: "failed"}).Item
			}
			reject(t, s, op, Request{Op: "complete-external", ID: task.ID, Version: task.Version, Item: Item{PR: externalPR(repo.Slug)}}, 409)
		})
	}
	s := service(t)
	repo, plan := repoPlan(t, s)
	for _, item := range []Item{repo, plan} {
		reject(t, s, op, Request{Op: "complete-external", ID: item.ID, Version: item.Version, Item: Item{PR: externalPR(repo.Slug)}}, 400)
	}
	reject(t, s, op, Request{Op: "complete-external", ID: "t999", Version: 1}, 404)
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
