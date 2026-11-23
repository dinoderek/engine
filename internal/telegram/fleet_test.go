package telegram

import (
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
)

// fleetQuestion has the coordinator ask about host mac, unreachable for
// UnreachableAsk with a task dispatched there.
func fleetQuestion(t *testing.T, l *Loop, plan string) control.Update {
	t.Helper()
	now := instant
	l.Service.Now = func() time.Time { return now }
	p := command(t, l.Service, control.Request{Op: "get", ID: plan}).Item
	task := command(t, l.Service, control.Request{Op: "create", Item: control.Item{Kind: "task", Repo: p.Repo, Plan: plan, Title: "Task", Body: "## Outcome\nWorks"}}).Item
	p = command(t, l.Service, control.Request{Op: "approve", ID: plan, Version: p.Version}).Item
	command(t, l.Service, control.Request{Op: "run", ID: plan, Version: p.Version})
	if _, err := l.Service.Dispatch(*task, "mac", "claude", "dispatch"); err != nil {
		t.Fatal(err)
	}
	gone := func(*control.State, control.Attempt) bool { return false }
	for _, at := range []time.Time{now, now.Add(control.UnreachableAsk)} {
		now = at
		if err := l.Service.SawReach("mac", false, gone); err != nil {
			t.Fatal(err)
		}
	}
	inbox := command(t, l.Service, control.Request{Op: "inbox"}).Updates
	if len(inbox) != 1 || inbox[0].Host != "mac" {
		t.Fatalf("inbox %+v", inbox)
	}
	return inbox[0]
}

func TestATapAnswersAFleetQuestion(t *testing.T) {
	l, f, plan := testLoop(t)
	q := fleetQuestion(t, l, plan)
	pass(t, l, instant)
	sent := f.calls["sendMessage"]
	if len(sent) != 1 || !strings.HasPrefix(sent[0]["text"].(string), "Host mac\nClass: environment") {
		t.Fatalf("sent %+v", sent)
	}
	if rows := sent[0]["reply_markup"].(map[string]any)["inline_keyboard"].([]any); len(rows) != 3 {
		t.Fatalf("buttons %+v", rows)
	}
	c := callback(q, 11, 99, "tap")
	c.Data = callbackData(q.ID, 1)
	tap(t, l, c)
	var answers []string
	for _, u := range command(t, l.Service, control.Request{Op: "updates"}).Updates {
		if u.Kind == "resolved" && u.ReplyTo == q.ID {
			answers = append(answers, u.Body)
		}
	}
	if len(answers) != 1 || answers[0] != control.ChoiceMove {
		t.Fatalf("answers %q", answers)
	}
	if text := f.calls["editMessageText"][0]["text"].(string); !strings.Contains(text, "Answered: "+control.ChoiceMove) {
		t.Fatal(text)
	}
}
