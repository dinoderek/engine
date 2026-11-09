package control

import "testing"

func TestOperatorInboxAcknowledgementReplyAndResolution(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	task := newTask(t, s, repo.ID, plan.ID)
	approve(t, s, plan)
	a, out := dispatch(t, s, task)
	question := call(t, s, a, Request{Op: "ask", ID: task.ID, Body: "Approve the API change?"}).Updates[0]
	current := call(t, s, op, Request{Op: "get", ID: task.ID}).Item
	if current.State != "needs-human" {
		t.Fatal(current)
	}
	call(t, s, op, Request{Op: "ack", ReplyTo: question.ID})
	if len(call(t, s, op, Request{Op: "inbox"}).Updates) != 1 {
		t.Fatal("acknowledgement hid unresolved question")
	}
	call(t, s, op, Request{Op: "reply", ReplyTo: question.ID, Body: "I am considering this."})
	reject(t, s, op, Request{Op: "state", ID: task.ID, Version: current.Version, State: "running"}, 409)
	reject(t, s, a, Request{Op: "resolve", ReplyTo: question.ID, Body: "self approved"}, 403)
	reject(t, s, a, Request{Op: "state", ID: task.ID, Version: current.Version, State: "running"}, 403)
	call(t, s, op, Request{Op: "resolve", ReplyTo: question.ID, Body: "Approved for this task revision."})
	if len(call(t, s, op, Request{Op: "inbox"}).Updates) != 0 {
		t.Fatal("resolved question remains open")
	}
	if got := call(t, s, op, Request{Op: "get", ID: task.ID}).Item.State; got != "running" {
		t.Fatalf("resolution left the task %s, want running", got)
	}
	info := call(t, s, a, Request{Op: "say", Body: "Host maintenance", Recipient: "operator"}).Updates[0]
	call(t, s, op, Request{Op: "ack", ReplyTo: info.ID})
	if len(call(t, s, op, Request{Op: "inbox"}).Updates) != 0 {
		t.Fatal("acknowledged information remains")
	}
	updates := call(t, s, op, Request{Op: "updates", ID: task.ID, After: question.ID}).Updates
	if len(updates) < 4 || updates[0].ID <= question.ID {
		t.Fatal("cursor failed")
	}
	if err := s.SetAttempt(out.Attempt.ID, "running", "@1"); err != nil {
		t.Fatal(err)
	}
	reject(t, s, a, Request{Op: "ask", ID: "absent", Body: "?"}, 404)
	reject(t, s, op, Request{Op: "reply", ReplyTo: 999, Body: "?"}, 404)
	reject(t, s, a, Request{Op: "say", Body: "?", Recipient: "stranger"}, 400)
	reject(t, s, a, Request{Op: "say", Body: "?", RequiresReply: true}, 400)
	reject(t, s, a, Request{Op: "say"}, 400)
	reject(t, s, op, Request{Op: "resolve", ReplyTo: info.ID}, 400)
}
