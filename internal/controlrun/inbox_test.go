package controlrun

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/reconcile"
	"github.com/dinoderek/engine/internal/session"
)

func TestPlanUpdatesWakeTheWaitingTaskSession(t *testing.T) {
	c, h, _, plan, task := setup(t)
	pass(t, c)
	pass(t, c)
	h.status.Windows[0].State = session.StateWaiting
	var texts []string
	c.Wake = func(_ reconcile.Session, text string) error { texts = append(texts, text); return nil }
	st, _ := snapshotOf(c.Service.Store)
	attempt := st.Items[task.ID].Attempt
	worker := control.Actor{Name: attempt, Role: "worker", Attempt: attempt}
	quiet := []struct {
		actor control.Actor
		req   control.Request
	}{
		{worker, control.Request{Op: "say", ID: plan.ID, Body: "My own note to the plan"}},
		{control.Actor{Name: "coordinator", Role: "coordinator"}, control.Request{Op: "say", ID: plan.ID, Body: "Automatic notice"}},
		{worker, control.Request{Op: "say", ID: plan.ID, Body: "For the operator", Recipient: "operator"}},
	}
	for _, q := range quiet {
		q.req.Key = control.Secret()
		if _, err := c.Service.Execute(q.actor, q.req); err != nil {
			t.Fatal(err)
		}
	}
	pass(t, c)
	if len(texts) != 0 {
		t.Fatalf("woke for %v", texts)
	}
	execute(t, c.Service, control.Request{Op: "say", ID: plan.ID, Body: "Every task may use the second approach"})
	pass(t, c)
	pass(t, c)
	if len(texts) != 1 {
		t.Fatalf("woke %d times, want once", len(texts))
	}
	for _, want := range []string{"engine updates " + task.ID + " --after", "engine updates " + plan.ID + " --after"} {
		if !strings.Contains(texts[0], want) {
			t.Fatalf("wake %q lacks %q", texts[0], want)
		}
	}
}

func TestPendingMessageCountsResolutionsOnlyForTheTasksPlan(t *testing.T) {
	task := control.Item{ID: "t3", Kind: "task", Plan: "p2"}
	planner := control.Item{ID: "p2", Kind: "plan"}
	at := control.Attempt{ID: "a1", Seen: 1}
	cases := []struct {
		name string
		item control.Item
		u    control.Update
		want int
	}{
		{"own say", task, control.Update{ID: 2, Item: "t3", Kind: "say", Author: "operator"}, 2},
		{"plan question", task, control.Update{ID: 2, Item: "p2", Kind: "ask", Author: "a2", Recipient: "operator"}, 0},
		{"plan resolution", task, control.Update{ID: 2, Item: "p2", Kind: "resolved", Author: "operator"}, 2},
		{"plan pane answer", task, control.Update{ID: 2, Item: "p2", Kind: control.KindPaneAnswer, Author: "a2"}, 2},
		{"own pane answer", task, control.Update{ID: 2, Item: "t3", Kind: control.KindPaneAnswer, Author: "a1"}, 0},
		{"plan pane message", task, control.Update{ID: 2, Item: "p2", Kind: control.KindPaneMessage, Author: "a2"}, 0},
		{"plan reply", task, control.Update{ID: 2, Item: "p2", Kind: "reply", Author: "operator"}, 0},
		{"plan state", task, control.Update{ID: 2, Item: "p2", Kind: "state", Author: "operator"}, 0},
		{"other plan", task, control.Update{ID: 2, Item: "p9", Kind: "resolved", Author: "operator"}, 0},
		{"resolution seen", task, control.Update{ID: 1, Item: "p2", Kind: "resolved", Author: "operator"}, 0},
		{"own author", task, control.Update{ID: 2, Item: "p2", Kind: "say", Author: "a1"}, 0},
		{"planner own plan", planner, control.Update{ID: 2, Item: "p2", Kind: "say", Author: "operator"}, 2},
		{"planner plan state", planner, control.Update{ID: 2, Item: "p2", Kind: "state", Author: "operator"}, 0},
	}
	for _, tc := range cases {
		store := emptyStore(t)
		mutate(t, store, func(tx *control.Tx) error {
			// Update IDs are the history's positions: the first update of a case
			// numbered 2 follows one that wakes nobody.
			if tc.u.ID == 2 {
				tx.AppendUpdate(control.Actor{Name: "filler"}, control.Request{ID: "t0"}, "state", "", time.Time{})
			}
			tx.AppendUpdate(control.Actor{Name: tc.u.Author}, control.Request{ID: tc.u.Item}, tc.u.Kind, "", time.Time{})
			tx.PutUpdate(tc.u)
			return nil
		})
		inTx(t, store, func(tx *control.Tx) {
			if got := pendingMessage(tx, tc.item, at); got != tc.want {
				t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

func TestPlanQuestionHoldsOnlyTheAskersSessionUntilItsResolutionWakesIt(t *testing.T) {
	c, h, repo, plan, task := setup(t)
	card := "## Outcome\nWorks\n## Tests\nProve it"
	sibling := execute(t, c.Service, control.Request{Op: "create", Item: control.Item{Kind: "task", Title: "Sibling", Repo: repo.ID, Plan: plan.ID, Body: card}})
	execute(t, c.Service, control.Request{Op: "create", Item: control.Item{Kind: "task", Title: "Acceptance", Repo: repo.ID, Plan: plan.ID, Body: card, Depends: []string{task.ID, sibling.ID}}})
	pass(t, c)
	pass(t, c)
	if len(h.status.Windows) != 2 {
		t.Fatalf("windows = %+v, want both tasks running", h.status.Windows)
	}
	for i := range h.status.Windows {
		h.status.Windows[i].State = session.StateWaiting
	}
	woken := map[string]int{}
	c.Wake = func(s reconcile.Session, _ string) error { woken[s.Name]++; return nil }
	st, _ := snapshotOf(c.Service.Store)
	attempt := st.Items[task.ID].Attempt
	worker := control.Actor{Name: attempt, Role: "worker", Attempt: attempt}
	out, err := c.Service.Execute(worker, control.Request{Op: "ask", Key: control.Secret(), ID: plan.ID, Body: "The unit lane is red on main"})
	if err != nil {
		t.Fatal(err)
	}
	execute(t, c.Service, control.Request{Op: "say", ID: plan.ID, Body: "Looking into it"})
	pass(t, c)
	if asker := fmt.Sprintf("demo-%d", control.Number(task.ID)); woken[asker] != 0 {
		t.Fatalf("woke %v while the plan question the asker put was open", woken)
	}
	if name := fmt.Sprintf("demo-%d", control.Number(sibling.ID)); woken[name] != 1 {
		t.Fatalf("woke %v, want the sibling woken once for the plan note", woken)
	}
	execute(t, c.Service, control.Request{Op: "resolve", ReplyTo: out.Updates[0].ID, Body: "Fixed on main; merge it in"})
	pass(t, c)
	st, _ = snapshotOf(c.Service.Store)
	if st.Items[task.ID].State != "running" {
		t.Fatalf("asker is %s, want running", st.Items[task.ID].State)
	}
	for id, want := range map[string]int{task.ID: 1, sibling.ID: 2} {
		if name := fmt.Sprintf("demo-%d", control.Number(id)); woken[name] != want {
			t.Fatalf("woken = %v, want %s woken %d times", woken, name, want)
		}
	}
}
