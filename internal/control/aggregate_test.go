package control

import (
	"net/http/httptest"
	"reflect"
	"slices"
	"testing"
)

// aggregateWorld is a repository with an approved plan, tasks in several
// states, a running session with an open question, and messages for the
// operator on a task, on the plan and from the coordinator.
type aggregateWorld struct {
	s                   Service
	repo, plan          Item
	running, idle, done Item
	worker, planner     Actor
	question            Update
}

func newAggregateWorld(t *testing.T) aggregateWorld {
	t.Helper()
	w := aggregateWorld{s: service(t)}
	w.repo, w.plan = repoPlan(t, w.s)
	w.running, w.idle, w.done = newTask(t, w.s, w.repo.ID, w.plan.ID), newTask(t, w.s, w.repo.ID, w.plan.ID), newTask(t, w.s, w.repo.ID, w.plan.ID)
	w.plan = approve(t, w.s, w.plan)
	w.planner, _ = dispatch(t, w.s, w.plan)
	w.worker, _ = dispatch(t, w.s, w.running)
	_, finished := dispatch(t, w.s, w.done)
	if err := w.s.SetAttempt(finished.Attempt.ID, "ended", ""); err != nil {
		t.Fatal(err)
	}
	w.question = call(t, w.s, w.worker, Request{Op: "ask", ID: w.running.ID, Body: "Approve?"}).Updates[0]
	call(t, w.s, w.worker, Request{Op: "say", ID: w.running.ID, Body: "Working", Recipient: "operator"})
	call(t, w.s, w.planner, Request{Op: "say", ID: w.plan.ID, Body: "Planned", Recipient: "operator"})
	call(t, w.s, Actor{Name: "coordinator", Role: "coordinator"}, Request{Op: "say", ID: w.idle.ID, Body: "Waiting", Recipient: "operator"})
	return w
}

func inboxPages(t *testing.T, s Service) []Update {
	t.Helper()
	var all []Update
	for cursor := 0; ; {
		page := call(t, s, op, Request{Op: "inbox", After: cursor}).Updates
		all = append(all, page...)
		if len(page) < 200 {
			return all
		}
		cursor = page[len(page)-1].ID
	}
}

func idsOf(items []Item) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.ID)
	}
	return out
}

func TestOverviewAnswersWhatTheSeparateReadsAnswer(t *testing.T) {
	w := newAggregateWorld(t)
	got := call(t, w.s, op, Request{Op: "overview"})
	if want := inboxPages(t, w.s); len(want) < 4 || !reflect.DeepEqual(got.Updates, want) {
		t.Fatalf("inbox = %+v, want what inbox returns: %+v", got.Updates, want)
	}
	if want := call(t, w.s, op, Request{Op: "session-view"}).Sessions; len(want) < 2 || !reflect.DeepEqual(got.Sessions, want) {
		t.Fatalf("sessions = %+v, want what session-view returns: %+v", got.Sessions, want)
	}
	var lists []Item
	for _, kind := range []string{"repo", "plan"} {
		lists = append(lists, call(t, w.s, op, Request{Op: "list", State: kind}).Items...)
	}
	for _, want := range lists {
		i := slices.IndexFunc(got.Items, func(it Item) bool { return it.ID == want.ID })
		if i < 0 || !reflect.DeepEqual(got.Items[i], want) {
			t.Fatalf("item %s = %+v, want what list returns: %+v", want.ID, got.Items, want)
		}
	}
	if want := []string{w.repo.ID, w.plan.ID, w.running.ID, w.idle.ID}; !slices.Equal(idsOf(got.Items), want) {
		t.Fatalf("items %v, want the repositories, plans and the tasks the inbox is about, by number: %v", idsOf(got.Items), want)
	}
	if plan := got.Items[1]; plan.Status == "" {
		t.Fatalf("plan %+v carries no status", plan)
	}
}

func TestOverviewInboxIsBoundedAndPagesRemain(t *testing.T) {
	w := newAggregateWorld(t)
	for range overviewInbox {
		call(t, w.s, w.planner, Request{Op: "say", ID: w.plan.ID, Body: "More", Recipient: "operator"})
	}
	got := call(t, w.s, op, Request{Op: "overview"})
	all := inboxPages(t, w.s)
	if len(got.Updates) != overviewInbox || !reflect.DeepEqual(got.Updates, all[:overviewInbox]) {
		t.Fatalf("overview carries %d inbox entries, want the first %d of %d", len(got.Updates), overviewInbox, len(all))
	}
}

func TestOverviewAndPlanDetailAreOperatorOnly(t *testing.T) {
	w := newAggregateWorld(t)
	for _, actor := range []Actor{w.worker, w.planner, {Name: "coordinator", Role: "coordinator"}} {
		reject(t, w.s, actor, Request{Op: "overview"}, 403)
		reject(t, w.s, actor, Request{Op: "plan-detail", ID: w.plan.ID}, 403)
	}
}

func TestPlanDetailAnswersWhatTheSeparateReadsAnswer(t *testing.T) {
	w := newAggregateWorld(t)
	got := call(t, w.s, op, Request{Op: "plan-detail", ID: w.plan.ID})
	if want := call(t, w.s, op, Request{Op: "plan-view", ID: w.plan.ID}).Items; !reflect.DeepEqual(got.Items, want) {
		t.Fatalf("items = %+v, want the plan view: %+v", got.Items, want)
	}
	mine := map[string]bool{w.plan.ID: true, w.running.ID: true, w.idle.ID: true, w.done.ID: true}
	var attempts []Attempt
	for _, at := range call(t, w.s, op, Request{Op: "attempts"}).Attempts {
		if it := call(t, w.s, op, Request{Op: "get", ID: at.Item}).Item; mine[at.Item] && it.Attempt == at.ID {
			attempts = append(attempts, at)
		}
	}
	if len(attempts) != 3 || !reflect.DeepEqual(got.Attempts, attempts) {
		t.Fatalf("attempts = %+v, want the current attempt of each item that has one: %+v", got.Attempts, attempts)
	}
	var rows []SessionRow
	for _, row := range call(t, w.s, op, Request{Op: "session-view"}).Sessions {
		if mine[row.Item] {
			rows = append(rows, row)
		}
	}
	if len(rows) != 2 || !reflect.DeepEqual(got.Sessions, rows) {
		t.Fatalf("sessions = %+v, want the session rows of those items: %+v", got.Sessions, rows)
	}
}

func TestPlanDetailLeavesOtherPlansOut(t *testing.T) {
	w := newAggregateWorld(t)
	other := *call(t, w.s, op, Request{Op: "create", Item: Item{Kind: "plan", Repo: w.repo.ID, Title: "Another", Body: "x"}}).Item
	task := newTask(t, w.s, w.repo.ID, other.ID)
	got := call(t, w.s, op, Request{Op: "plan-detail", ID: other.ID})
	if want := []string{other.ID, w.repo.ID, task.ID}; !slices.Equal(idsOf(got.Items), want) || len(got.Attempts) != 0 || len(got.Sessions) != 0 {
		t.Fatalf("plan detail of an idle plan = %+v, want only its own records", got)
	}
	reject(t, w.s, op, Request{Op: "plan-detail", ID: w.running.ID}, 404)
	reject(t, w.s, op, Request{Op: "plan-detail", ID: "p999"}, 404)
}

func TestUpdatesCarryQuestionStatuses(t *testing.T) {
	w := newAggregateWorld(t)
	second := call(t, w.s, w.worker, Request{Op: "ask", ID: w.running.ID, Body: "And this?"}).Updates[0]
	statuses := func(item string, after int) []QuestionStatus {
		return call(t, w.s, op, Request{Op: "updates", ID: item, After: after}).Questions
	}
	want := []QuestionStatus{{ID: w.question.ID, State: "open"}, {ID: second.ID, State: "open"}}
	if got := statuses(w.running.ID, 0); !reflect.DeepEqual(got, want) {
		t.Fatalf("statuses = %+v, want %+v", got, want)
	}
	call(t, w.s, op, Request{Op: "ack", ReplyTo: w.question.ID})
	call(t, w.s, op, Request{Op: "reply", ReplyTo: w.question.ID, Body: "Looking"})
	call(t, w.s, op, Request{Op: "reply", ReplyTo: w.question.ID, Body: "Still looking"})
	decision := call(t, w.s, op, Request{Op: "resolve", ReplyTo: w.question.ID, Body: "Yes"}).Updates[0]
	want[0] = QuestionStatus{ID: w.question.ID, State: "resolved", Acknowledged: true, Replies: 2, Resolution: decision.ID}
	if got := statuses(w.running.ID, 0); !reflect.DeepEqual(got, want) {
		t.Fatalf("after answers = %+v, want %+v", got, want)
	}
	// A page past every question still settles them: statuses are as of now.
	if got := statuses(w.running.ID, decision.ID); !reflect.DeepEqual(got, want) {
		t.Fatalf("after the cursor = %+v, want every question of the item: %+v", got, want)
	}
	if got := statuses(w.plan.ID, 0); got == nil || len(got) != 0 {
		t.Fatalf("plan statuses = %#v, want an empty list: its tasks' questions are theirs", got)
	}
	if got := statuses("", 0); got != nil {
		t.Fatalf("an updates read of every item carried statuses %+v", got)
	}
}

func TestPlanQuestionStatusStaysOnThePlan(t *testing.T) {
	w := newAggregateWorld(t)
	asked := call(t, w.s, w.worker, Request{Op: "ask", ID: w.plan.ID, Body: "Scope?"}).Updates[0]
	got := call(t, w.s, op, Request{Op: "updates", ID: w.plan.ID}).Questions
	if want := []QuestionStatus{{ID: asked.ID, State: "open"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("plan statuses = %+v, want %+v", got, want)
	}
}

func TestAPaneAnswerResolvesAQuestionStatus(t *testing.T) {
	w := newAggregateWorld(t)
	var answer Update
	err := w.s.write(func(tx *Tx) error {
		answer = tx.AppendUpdate(w.worker, Request{ID: w.running.ID, ReplyTo: w.question.ID}, KindPaneAnswer, "Yes", w.s.now())
		return tx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	got := call(t, w.s, op, Request{Op: "updates", ID: w.running.ID, After: answer.ID}).Questions
	if want := []QuestionStatus{{ID: w.question.ID, State: "resolved", Resolution: answer.ID}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("statuses = %+v, want %+v", got, want)
	}
}

// A client tells a server that sends question statuses from an older one by
// the field's presence, so an item with no question still sends it, empty.
func TestAnUpdatesReadWithoutQuestionsSendsAnEmptyStatusList(t *testing.T) {
	w := newAggregateWorld(t)
	token := Secret()
	server := httptest.NewServer(Handler(w.s, token, t.Logf))
	defer server.Close()
	client := Client{URL: server.URL, Token: token}
	got, err := client.Call(t.Context(), Request{Op: "updates", ID: w.idle.ID})
	if err != nil || got.Questions == nil || len(got.Questions) != 0 {
		t.Fatalf("questions = %#v (%v), want an empty, non-nil list", got.Questions, err)
	}
	if got, err = client.Call(t.Context(), Request{Op: "updates"}); err != nil || got.Questions != nil {
		t.Fatalf("an updates read of every item sent statuses %#v (%v)", got.Questions, err)
	}
	if got, err = client.Call(t.Context(), Request{Op: "inbox"}); err != nil || got.Questions != nil {
		t.Fatalf("an inbox read sent statuses %#v (%v)", got.Questions, err)
	}
}
