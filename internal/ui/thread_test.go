package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
)

var t0 = time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)

func upd(id int, item, kind, body string) control.Update {
	return control.Update{ID: id, Item: item, Author: "operator", At: t0.Add(time.Duration(id) * time.Minute), Kind: kind, Body: body}
}

func replyTo(id int, item, kind string, to int) control.Update {
	u := upd(id, item, kind, "")
	u.ReplyTo = to
	return u
}

func ask(id int, item string) control.Update {
	u := upd(id, item, "ask", "Approve?")
	u.Recipient, u.RequiresReply = "operator", true
	return u
}

func messageIDs(th Thread) []int {
	out := []int{}
	for _, m := range th.Messages {
		out = append(out, m.ID)
	}
	return out
}

func TestThreadShowsMessagesOnlyForItsItem(t *testing.T) {
	updates := []control.Update{
		upd(1, "t7", "created", "Add things"),
		upd(2, "t7", "say", "Use the second approach"),
		upd(3, "p2", "say", "Plan-level note"),
		upd(4, "t7", "state", "running (version 3)"),
		upd(5, "t7", "dispatch", "a1"),
		upd(6, "t7", "execution", "a1: running"),
		upd(7, "t7", "lock-acquired", "a1"),
		upd(8, "", "say", "Host problem"),
		replyTo(9, "t7", "acknowledged", 2),
		upd(10, "t7", "say", "<b>not markup</b>"),
		replyTo(11, "p2", "ruling", 2),
	}
	th := threadWindow("t7", updates, 0, 0)
	if want := []int{2, 9, 10}; !reflect.DeepEqual(messageIDs(th), want) {
		t.Fatalf("messages = %v, want %v", messageIDs(th), want)
	}
	if th.Messages[2].Body != "<b>not markup</b>" || th.Messages[0].Author != "operator" || !th.Messages[0].At.Equal(t0.Add(2*time.Minute)) {
		t.Fatalf("message = %+v, want body, author and time carried as is", th.Messages)
	}
	if plan := threadWindow("p2", updates, 0, 0); !reflect.DeepEqual(messageIDs(plan), []int{3, 11}) {
		t.Fatalf("plan thread = %v, want only its own message", messageIDs(plan))
	}
}

func TestEmptyThreadEncodesEmptyLists(t *testing.T) {
	th := threadWindow("t7", []control.Update{upd(1, "t7", "created", "x")}, 0, 0)
	b, _ := json.Marshal(th)
	if want := `{"item":"t7","messages":[],"questions":[],"next":0,"more":false}`; string(b) != want {
		t.Fatalf("empty thread = %s, want %s", b, want)
	}
}

func TestQuestionsStayOpenUntilResolved(t *testing.T) {
	updates := []control.Update{
		ask(1, "t7"), replyTo(2, "t7", "acknowledged", 1),
		upd(3, "t7", "reply", "Looking"), ask(4, "t7"),
	}
	updates[2].ReplyTo = 1
	th := threadWindow("t7", updates, 0, 0)
	want := []Question{{ID: 1, State: "open", Acknowledged: true, Replies: 1}, {ID: 4, State: "open"}}
	if !reflect.DeepEqual(th.Questions, want) {
		t.Fatalf("questions = %+v, want %+v", th.Questions, want)
	}
	decision := upd(5, "t7", "resolved", "Approved")
	decision.ReplyTo = 1
	th = threadWindow("t7", append(updates, decision), 0, 0)
	want[0] = Question{ID: 1, State: "resolved", Acknowledged: true, Replies: 1, Resolution: 5}
	if !reflect.DeepEqual(th.Questions, want) {
		t.Fatalf("after a resolution = %+v, want %+v", th.Questions, want)
	}
}

func TestResolutionOutsideTheWindowStillSettlesEarlierQuestions(t *testing.T) {
	decision := upd(3, "t7", "resolved", "Go")
	decision.ReplyTo = 1
	th := threadWindow("t7", []control.Update{ask(1, "t7"), upd(2, "t7", "say", "x"), decision}, 2, 0)
	if !reflect.DeepEqual(messageIDs(th), []int{3}) || th.Next != 3 {
		t.Fatalf("window = %v next %d, want the decision after the cursor", messageIDs(th), th.Next)
	}
	if want := []Question{{ID: 1, State: "resolved", Resolution: 3}}; !reflect.DeepEqual(th.Questions, want) {
		t.Fatalf("questions = %+v, want the earlier question resolved", th.Questions)
	}
}

func TestPlanQuestionDoesNotOpenATaskQuestion(t *testing.T) {
	th := threadWindow("t7", []control.Update{ask(1, "p2"), upd(2, "t7", "say", "hi")}, 0, 0)
	if len(th.Questions) != 0 || !reflect.DeepEqual(messageIDs(th), []int{2}) {
		t.Fatalf("task thread = %+v, want the plan's question kept out", th)
	}
}

func manyMessages(n int) []control.Update {
	var out []control.Update
	for i := 1; i <= n; i++ {
		if i%3 == 0 {
			out = append(out, upd(i*10, "t7", "state", "noise"))
		}
		out = append(out, upd(i, "t7", "say", fmt.Sprintf("m%d", i)))
	}
	return out
}

func TestThreadPagesByCursor(t *testing.T) {
	updates := manyMessages(pageSize*2 + 5)
	first := threadWindow("t7", updates, 0, 0)
	if len(first.Messages) != pageSize || !first.More || first.Next != pageSize {
		t.Fatalf("first page = %d messages, more %v, next %d", len(first.Messages), first.More, first.Next)
	}
	second := threadWindow("t7", updates, first.Next, 0)
	if second.Messages[0].ID != pageSize+1 || len(second.Messages) != pageSize || !second.More {
		t.Fatalf("second page starts at %d with %d messages, more %v", second.Messages[0].ID, len(second.Messages), second.More)
	}
	last := threadWindow("t7", updates, second.Next, 0)
	if len(last.Messages) != 5 || last.More || last.Next != pageSize*2+5 {
		t.Fatalf("last page = %d messages, more %v, next %d", len(last.Messages), last.More, last.Next)
	}
	idle := threadWindow("t7", updates, last.Next, 0)
	if len(idle.Messages) != 0 || idle.More || idle.Next != last.Next {
		t.Fatalf("after the end = %+v, want nothing new and the cursor kept", idle)
	}
}

func TestAnchorExtendsTheWindow(t *testing.T) {
	updates := manyMessages(pageSize * 3)
	th := threadWindow("t7", updates, 0, pageSize+7)
	if len(th.Messages) != pageSize+7 || th.Messages[len(th.Messages)-1].ID != pageSize+7 || !th.More {
		t.Fatalf("anchored window = %d messages ending at %d, more %v", len(th.Messages), th.Messages[len(th.Messages)-1].ID, th.More)
	}
	inside := threadWindow("t7", updates, 0, 3)
	if len(inside.Messages) != pageSize {
		t.Fatalf("anchor inside the first page gave %d messages, want %d", len(inside.Messages), pageSize)
	}
	unknown := threadWindow("t7", updates, 0, 99999)
	if len(unknown.Messages) != pageSize {
		t.Fatalf("unknown anchor gave %d messages, want %d", len(unknown.Messages), pageSize)
	}
}

func TestItemDetailRendersDescription(t *testing.T) {
	d := itemDetail(control.Item{ID: "t7", Kind: "task", Repo: "r1", Plan: "p2", Title: "T", State: "todo", Body: "## Outcome\n\n- a"})
	if d.Plan != "p2" || len(d.Description) != 2 || d.Description[0].Type != "heading" {
		t.Fatalf("detail = %+v", d)
	}
	b, _ := json.Marshal(itemDetail(control.Item{ID: "p2", Kind: "plan"}))
	if !strings.Contains(string(b), `"description":[]`) {
		t.Fatalf("no body encodes as %s, want an empty description list", b)
	}
}

// pagedCaller serves item reads and 200-update pages from a fixed update list.
type pagedCaller struct {
	updates []control.Update
	reads   []control.Request
	fail    error
}

func (p *pagedCaller) Call(_ context.Context, r control.Request) (control.Response, error) {
	p.reads = append(p.reads, r)
	if p.fail != nil {
		return control.Response{}, p.fail
	}
	if r.Op == "get" {
		return control.Response{Item: &control.Item{ID: r.ID, Kind: "task", Title: "T", Body: "body"}}, nil
	}
	var out control.Response
	for _, u := range p.updates {
		if u.ID > r.After && u.Item == r.ID && len(out.Updates) < 200 {
			out.Updates = append(out.Updates, u)
		}
	}
	return out, nil
}

func TestThreadReadsEveryPageOfUpdates(t *testing.T) {
	var all []control.Update
	for i := 1; i <= 450; i++ {
		all = append(all, upd(i, "t7", "say", "m"))
	}
	pc := &pagedCaller{updates: all}
	env, status := newUpstream(pc, func() time.Time { return t0 }, nil).thread(context.Background(), "t7", 100, 0)
	th, _ := env.Data.(Thread)
	if status != 200 || len(th.Messages) != pageSize || th.Messages[0].ID != 101 || !th.More {
		t.Fatalf("thread = %d %d messages from %d", status, len(th.Messages), th.Messages[0].ID)
	}
	var after []int
	for _, r := range pc.reads {
		if r.Op == "updates" {
			after = append(after, r.After)
		}
	}
	if !reflect.DeepEqual(after, []int{0, 200, 400}) {
		t.Fatalf("update reads after %v, want 0, 200, 400", after)
	}
}

func TestThreadTooLongToReadIsAnError(t *testing.T) {
	var all []control.Update
	for i := 1; i <= 200*maxThreadReads+1; i++ {
		all = append(all, upd(i, "t7", "say", "m"))
	}
	pc := &pagedCaller{updates: all}
	env, status := newUpstream(pc, func() time.Time { return t0 }, nil).thread(context.Background(), "t7", 0, 0)
	if status != 502 || env.Data != nil || !strings.Contains(env.Error, "too long") {
		t.Fatalf("overlong thread = %d %+v, want an error rather than a partial thread", status, env)
	}
}
