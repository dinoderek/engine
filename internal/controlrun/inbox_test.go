package controlrun

import (
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/run"
	"github.com/dinoderek/engine/internal/session"
)

func TestPlanUpdatesWakeTheWaitingTaskSession(t *testing.T) {
	c, h, _, plan, task := setup(t)
	pass(t, c)
	pass(t, c)
	h.status.Windows[0].State = session.StateWaiting
	var texts []string
	c.Wake = func(_ run.Session, text string) error { texts = append(texts, text); return nil }
	st, _ := c.Service.Store.Snapshot()
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

func TestPendingMessageCountsRulingsOnlyForTheTasksPlan(t *testing.T) {
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
		{"plan ruling", task, control.Update{ID: 2, Item: "p2", Kind: "ruling", Author: "operator"}, 2},
		{"plan resolution", task, control.Update{ID: 2, Item: "p2", Kind: "resolved", Author: "operator"}, 2},
		{"plan reply", task, control.Update{ID: 2, Item: "p2", Kind: "reply", Author: "operator"}, 0},
		{"plan state", task, control.Update{ID: 2, Item: "p2", Kind: "state", Author: "operator"}, 0},
		{"other plan", task, control.Update{ID: 2, Item: "p9", Kind: "ruling", Author: "operator"}, 0},
		{"own ruling seen", task, control.Update{ID: 1, Item: "p2", Kind: "ruling", Author: "operator"}, 0},
		{"own author", task, control.Update{ID: 2, Item: "p2", Kind: "say", Author: "a1"}, 0},
		{"planner own plan", planner, control.Update{ID: 2, Item: "p2", Kind: "say", Author: "operator"}, 2},
		{"planner ruling", planner, control.Update{ID: 2, Item: "p2", Kind: "ruling", Author: "operator"}, 0},
	}
	for _, tc := range cases {
		if got := pendingMessage(control.State{Updates: []control.Update{tc.u}}, tc.item, at); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
}
