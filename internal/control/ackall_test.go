package control

import (
	"slices"
	"testing"
)

// inboxIDs is the operator inbox's update numbers.
func inboxIDs(t *testing.T, s Service) []int {
	t.Helper()
	var ids []int
	for _, u := range call(t, s, op, Request{Op: "inbox"}).Updates {
		ids = append(ids, u.ID)
	}
	return ids
}

func TestAckAllAcknowledgesOnlyMessagesInScopeAndReplaysOnce(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	other := *call(t, s, op, Request{Op: "create", Item: Item{Kind: "repo", Title: "Other", Slug: "owner/other", Remote: "git@example.test:owner/other.git"}}).Item
	otherPlan := *call(t, s, op, Request{Op: "create", Item: Item{Kind: "plan", Repo: other.ID, Title: "Elsewhere", Body: "Agreed"}}).Item
	tell := func(id, body string) int {
		return call(t, s, op, Request{Op: "say", ID: id, Body: body, Recipient: "operator"}).Updates[0].ID
	}
	onPlan := tell(plan.ID, "plan notice")
	onRepo := tell(repo.ID, "repo notice")
	question := call(t, s, op, Request{Op: "ask", ID: plan.ID, Body: "Which way?"}).Updates[0].ID
	elsewhere := tell(otherPlan.ID, "other notice")
	loose := tell("", "fleet notice")
	later := tell(plan.ID, "later notice")
	settled := tell(plan.ID, "settled notice")
	call(t, s, op, Request{Op: "resolve", ReplyTo: settled, Body: "handled"})

	scoped := Request{Op: "ack-all", Item: Item{Repo: repo.ID}, Before: later, Key: "bulk-1"}
	out := call(t, s, op, scoped)
	var acked []int
	for _, u := range out.Updates {
		if u.Kind != "acknowledged" || u.Author != "operator" {
			t.Fatalf("not an acknowledgement: %+v", u)
		}
		acked = append(acked, u.ReplyTo)
	}
	if !slices.Equal(acked, []int{onPlan, onRepo}) {
		t.Fatalf("acknowledged %v, want %v", acked, []int{onPlan, onRepo})
	}
	if got, want := inboxIDs(t, s), []int{question, elsewhere, loose, later}; !slices.Equal(got, want) {
		t.Fatalf("inbox %v, want %v", got, want)
	}
	count := len(call(t, s, op, Request{Op: "updates"}).Updates)
	if replay := call(t, s, op, scoped); len(replay.Updates) != 2 || replay.Updates[0].ID != out.Updates[0].ID {
		t.Fatalf("replay %+v", replay.Updates)
	}
	if len(call(t, s, op, Request{Op: "updates"}).Updates) != count {
		t.Fatal("a replay wrote again")
	}
	reject(t, s, op, Request{Op: "ack-all", Key: "bulk-1"}, 409)

	call(t, s, op, Request{Op: "ack-all"})
	for _, u := range call(t, s, op, Request{Op: "updates", After: settled}).Updates {
		if u.Kind == "acknowledged" && u.ReplyTo == settled {
			t.Fatal("acknowledged a message the inbox no longer lists")
		}
	}
	if got := inboxIDs(t, s); !slices.Equal(got, []int{question}) {
		t.Fatalf("inbox after acknowledging everything %v, want only the question", got)
	}
	if again := call(t, s, op, Request{Op: "ack-all"}); len(again.Updates) != 0 {
		t.Fatalf("acknowledged twice: %+v", again.Updates)
	}
}

func TestAckAllRefusals(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	task := newTask(t, s, repo.ID, plan.ID)
	approve(t, s, plan)
	worker, _ := dispatch(t, s, task)
	call(t, s, worker, Request{Op: "say", ID: task.ID, Body: "notice", Recipient: "operator"})
	reject(t, s, worker, Request{Op: "ack-all"}, 403)
	reject(t, s, op, Request{Op: "ack-all", Item: Item{Repo: plan.ID}}, 404)
	reject(t, s, op, Request{Op: "ack-all", Item: Item{Repo: "r99"}}, 404)
	reject(t, s, op, Request{Op: "ack-all", Before: -1}, 400)
	reject(t, s, op, Request{Op: "ack-all", ReplyTo: 1}, 400)
	if len(inboxIDs(t, s)) != 1 {
		t.Fatal("a refused ack-all acknowledged")
	}
}
