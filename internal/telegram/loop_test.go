package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
)

type botServer struct {
	mu       sync.Mutex
	calls    map[string][]map[string]any
	failures map[string]int
	updates  []Incoming
	next     int
	onCall   func(string, map[string]any)
}

func (f *botServer) setFailures(method string, count int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures[method] = count
}

func fakeBot(t *testing.T) (Bot, *botServer) {
	t.Helper()
	f := &botServer{calls: map[string][]map[string]any{}, failures: map[string]int{}, next: 10}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		method := strings.TrimPrefix(r.URL.Path, "/bottoken/")
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		f.calls[method] = append(f.calls[method], body)
		if f.onCall != nil {
			f.onCall(method, body)
		}
		w.Header().Set("Content-Type", "application/json")
		if f.failures[method] > 0 {
			f.failures[method]--
			_, _ = fmt.Fprint(w, `{"ok":false,"error_code":500}`)
			return
		}
		var result any = true
		switch method {
		case "sendMessage":
			f.next++
			result = map[string]int{"message_id": f.next}
		case "getUpdates":
			result = f.updates
			f.updates = nil
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
	}))
	t.Cleanup(server.Close)
	return Bot{Token: "token", BaseURL: server.URL, HTTP: server.Client()}, f
}
func testLoop(t *testing.T) (*Loop, *botServer, string) {
	t.Helper()
	l, f, plan := unpairedLoop(t)
	// The pairing pass: what follows is new since the integration started.
	pass(t, l, time.Time{})
	return l, f, plan
}

// unpairedLoop is a loop that has never passed: no delivery cursor yet.
func unpairedLoop(t *testing.T) (*Loop, *botServer, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "control.db")
	store, err := control.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	svc := control.Service{Store: store}
	repo := command(t, svc, control.Request{Op: "create", Item: control.Item{Kind: "repo", Title: "Repo", Slug: "a/b", Remote: "https://example.test/a/b"}}).Item
	plan := command(t, svc, control.Request{Op: "create", Item: control.Item{Kind: "plan", Repo: repo.ID, Title: "Plan", Body: "requirements"}}).Item
	bot, f := fakeBot(t)
	return &Loop{Service: svc, Bot: bot, Chat: 99, Dashboard: "https://dashboard.test"}, f, plan.ID
}
func command(t *testing.T, s control.Service, r control.Request) control.Response {
	t.Helper()
	if r.Key == "" {
		r.Key = control.Secret()
	}
	out, err := s.Execute(operator, r)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func ask(t *testing.T, l *Loop, plan string) control.Update {
	t.Helper()
	zero := 0
	return command(t, l.Service, control.Request{Op: "ask", ID: plan, Body: "Question <plain>", Choices: []string{"yes", "no"}, Recommended: &zero, Class: "product"}).Updates[0]
}

// deliveries is the store's Telegram state without the pairing pass's
// marker row.
func deliveries(store *control.Store) ([]control.TelegramDelivery, int64, time.Time, error) {
	rows, offset, at, err := store.TelegramState()
	if len(rows) > 0 && rows[0].Update == 0 {
		rows = rows[1:]
	}
	return rows, offset, at, err
}

var instant = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func pass(t *testing.T, l *Loop, at time.Time) {
	t.Helper()
	if err := l.Pass(context.Background(), at); err != nil {
		t.Fatal(err)
	}
}
func callback(q control.Update, message int64, chat int64, id string) Callback {
	m := &Message{ID: message}
	m.Chat.ID = chat
	return Callback{ID: id, Data: callbackData(q.ID, 0), Message: m}
}
func tap(t *testing.T, l *Loop, c Callback) {
	t.Helper()
	if err := l.tap(context.Background(), c); err != nil {
		t.Fatal(err)
	}
}

func TestQuestionSendsOnceRetriesAndRestarts(t *testing.T) {
	l, f, plan := testLoop(t)
	q := ask(t, l, plan)
	f.setFailures("sendMessage", 1)
	if err := l.Pass(context.Background(), instant); err == nil {
		t.Fatal("failed send succeeded")
	}
	rows, _, _, err := deliveries(l.Service.Store)
	if err != nil || len(rows) != 1 || rows[0].Message != 0 {
		t.Fatalf("pending rows %+v %v", rows, err)
	}
	pass(t, l, instant)
	pass(t, l, instant)
	path := filepath.Join(t.TempDir(), "copy.db")
	if err = l.Service.Store.Backup(path); err != nil {
		t.Fatal(err)
	}
	store, err := control.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	l.Service.Store = store
	pass(t, l, instant)
	ask(t, l, plan)
	pass(t, l, instant)
	if len(f.calls["sendMessage"]) != 3 {
		t.Fatalf("sends %+v", f.calls)
	}
	body := f.calls["sendMessage"][1]
	if body["disable_notification"] != false || strings.Contains(body["text"].(string), "<b>") || body["parse_mode"] != nil {
		t.Fatal(body)
	}
	rows, _, _, _ = deliveries(store)
	if rows[0].Update != q.ID || rows[0].Message == 0 || len(rows) != 2 {
		t.Fatalf("rows %+v", rows)
	}
}

func TestDigestBatchesRateLimitsAndEditsAcknowledgements(t *testing.T) {
	l, f, plan := testLoop(t)
	first := command(t, l.Service, control.Request{Op: "say", ID: plan, Recipient: "operator", Body: "one"}).Updates[0]
	command(t, l.Service, control.Request{Op: "say", ID: plan, Recipient: "operator", Body: "two"})
	pass(t, l, instant)
	command(t, l.Service, control.Request{Op: "say", ID: plan, Recipient: "operator", Body: "three"})
	pass(t, l, instant.Add(59*time.Second))
	if len(f.calls["sendMessage"]) != 1 {
		t.Fatal("digest sent too early")
	}
	body := f.calls["sendMessage"][0]
	if body["disable_notification"] != true || !strings.Contains(body["text"].(string), "one") || !strings.Contains(body["text"].(string), "two") {
		t.Fatal(body)
	}
	command(t, l.Service, control.Request{Op: "ack", ReplyTo: first.ID})
	pass(t, l, instant.Add(time.Minute))
	if len(f.calls["sendMessage"]) != 2 || len(f.calls["editMessageText"]) != 1 {
		t.Fatalf("calls %+v", f.calls)
	}
	text := f.calls["editMessageText"][0]["text"].(string)
	if !strings.Contains(text, "one\nAcknowledged") || !strings.Contains(text, "two") {
		t.Fatal(text)
	}
	pass(t, l, instant.Add(time.Minute))
	if len(f.calls["editMessageText"]) != 1 {
		t.Fatal("repeated settlement")
	}
}

func TestCallbackChatAndMessageBindingDeduplicatesResolution(t *testing.T) {
	l, f, plan := testLoop(t)
	q := ask(t, l, plan)
	pass(t, l, instant)
	for _, c := range []Callback{callback(q, 11, 98, "foreign"), callback(q, 12, 99, "wrong-message"), {ID: "bad", Message: &Message{}}} {
		tap(t, l, c)
	}
	inbox := command(t, l.Service, control.Request{Op: "inbox"})
	if len(inbox.Updates) != 1 {
		t.Fatal("foreign callback resolved question")
	}
	c := callback(q, 11, 99, "callback-id")
	tap(t, l, c)
	tap(t, l, c)
	updates := command(t, l.Service, control.Request{Op: "updates", ID: plan})
	resolutions := 0
	for _, u := range updates.Updates {
		if u.Kind == "resolved" {
			resolutions++
			if u.Body != "yes" {
				t.Fatal(u)
			}
		}
	}
	if resolutions != 1 {
		t.Fatalf("resolutions %d", resolutions)
	}
	if text := f.calls["editMessageText"][1]["text"].(string); !strings.Contains(text, "Answered: yes") {
		t.Fatal(text)
	}
	pass(t, l, instant)
	if len(f.calls["editMessageText"]) != 2 {
		t.Fatal("settled callback edited again")
	}
}

func TestExternalResolutionAndAlreadyAnsweredTap(t *testing.T) {
	for _, byTap := range []bool{false, true} {
		t.Run(fmt.Sprint(byTap), func(t *testing.T) {
			l, f, plan := testLoop(t)
			q := ask(t, l, plan)
			pass(t, l, instant)
			command(t, l.Service, control.Request{Op: "resolve", ReplyTo: q.ID, Body: "elsewhere"})
			if byTap {
				tap(t, l, callback(q, 11, 99, "late"))
			} else {
				pass(t, l, instant)
			}
			text := f.calls["editMessageText"][0]["text"].(string)
			want := "Answered: elsewhere"
			if byTap {
				want = "Already answered"
			}
			if !strings.Contains(text, want) {
				t.Fatalf("%q lacks %q", text, want)
			}
		})
	}
}

func TestPendingQuestionSettledBeforeRetryIsNotSent(t *testing.T) {
	l, f, plan := testLoop(t)
	q := ask(t, l, plan)
	f.setFailures("sendMessage", 1)
	_ = l.Pass(context.Background(), instant)
	command(t, l.Service, control.Request{Op: "resolve", ReplyTo: q.ID, Body: "settled"})
	pass(t, l, instant)
	if len(f.calls["sendMessage"]) != 1 {
		t.Fatal("sent settled question")
	}
}

func TestPendingSendAndDigestThrottleSurviveReopen(t *testing.T) {
	l, f, plan := testLoop(t)
	command(t, l.Service, control.Request{Op: "say", ID: plan, Recipient: "operator", Body: "first digest"})
	pass(t, l, instant)
	command(t, l.Service, control.Request{Op: "say", ID: plan, Recipient: "operator", Body: "pending digest"})
	q := ask(t, l, plan)
	f.setFailures("sendMessage", 1)
	if err := l.Pass(context.Background(), instant.Add(time.Second)); err == nil {
		t.Fatal("expected failed send")
	}
	path := filepath.Join(t.TempDir(), "restart.db")
	if err := l.Service.Store.Backup(path); err != nil {
		t.Fatal(err)
	}
	store, err := control.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	l.Service.Store = store
	pass(t, l, instant.Add(2*time.Second))
	if len(f.calls["sendMessage"]) != 3 {
		t.Fatal("restart resent first digest or skipped pending question")
	}
	rows, _, _, err := deliveries(store)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range rows {
		if d.Update == q.ID && d.Message != 0 {
			found = true
		}
	}
	if !found {
		t.Fatal("pending question skipped")
	}
	pass(t, l, instant.Add(time.Minute))
	if len(f.calls["sendMessage"]) != 4 || !strings.Contains(f.calls["sendMessage"][3]["text"].(string), "pending digest") {
		t.Fatal("pending digest skipped")
	}
}

func TestRunPollsAndPersistsOffsetBeforeRestart(t *testing.T) {
	l, f, plan := testLoop(t)
	q := ask(t, l, plan)
	pass(t, l, instant)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := callback(q, 11, 99, "durable-callback")
	f.updates = []Incoming{{ID: 700, Callback: &c}}
	f.onCall = func(method string, _ map[string]any) {
		if method == "answerCallbackQuery" {
			cancel()
		}
	}
	l.Run(ctx)
	rows, offset, _, err := deliveries(l.Service.Store)
	if err != nil || offset != 701 || len(rows) != 1 || !rows[0].Settled {
		t.Fatalf("poll state %+v %d %v", rows, offset, err)
	}
	if got := f.calls["getUpdates"][0]["offset"].(float64); got != 0 {
		t.Fatalf("first offset %v", got)
	}
	path := filepath.Join(t.TempDir(), "restart.db")
	if err = l.Service.Store.Backup(path); err != nil {
		t.Fatal(err)
	}
	store, err := control.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	l.Service.Store = store
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	f.onCall = func(method string, _ map[string]any) {
		if method == "getUpdates" {
			cancel2()
		}
	}
	l.poll(ctx2)
	if got := f.calls["getUpdates"][1]["offset"].(float64); got != 701 {
		t.Fatalf("restart offset %v", got)
	}
}

func TestEditFailureRetriesSettlementWithoutResending(t *testing.T) {
	l, f, plan := testLoop(t)
	q := ask(t, l, plan)
	pass(t, l, instant)
	command(t, l.Service, control.Request{Op: "resolve", ReplyTo: q.ID, Body: "answer"})
	f.setFailures("editMessageText", 1)
	if err := l.Pass(context.Background(), instant); err == nil {
		t.Fatal("expected failed edit")
	}
	rows, _, _, _ := deliveries(l.Service.Store)
	if rows[0].Settled {
		t.Fatal("failed edit retired delivery")
	}
	pass(t, l, instant)
	if len(f.calls["sendMessage"]) != 1 || len(f.calls["editMessageText"]) != 2 {
		t.Fatalf("calls %+v", f.calls)
	}
}

func TestCallbackEditFailureReplaysFirstResolution(t *testing.T) {
	l, f, plan := testLoop(t)
	q := ask(t, l, plan)
	pass(t, l, instant)
	c := callback(q, 11, 99, "retry-callback")
	f.setFailures("editMessageText", 1)
	if err := l.tap(context.Background(), c); err != nil {
		t.Fatal("presentation failure blocked resolved callback:", err)
	}
	tap(t, l, c)
	updates := command(t, l.Service, control.Request{Op: "updates", ID: plan})
	count := 0
	for _, u := range updates.Updates {
		if u.Kind == "resolved" {
			count++
		}
	}
	if count != 1 || !strings.Contains(f.calls["editMessageText"][1]["text"].(string), "Answered: yes") {
		t.Fatal("callback replay changed resolution")
	}
}

func TestPermanentEditFailureDoesNotBlockLaterDeliveries(t *testing.T) {
	l, f, plan := testLoop(t)
	q := ask(t, l, plan)
	pass(t, l, instant)
	command(t, l.Service, control.Request{Op: "resolve", ReplyTo: q.ID, Body: "first answer"})
	f.setFailures("editMessageText", 1000)
	ask(t, l, plan)
	command(t, l.Service, control.Request{Op: "say", ID: plan, Recipient: "operator", Body: "new digest"})
	if err := l.Pass(context.Background(), instant); err == nil {
		t.Fatal("failed edit was not reported")
	}
	if len(f.calls["sendMessage"]) != 3 {
		t.Fatalf("old failed edit blocked later question/digest: %+v", f.calls)
	}
	path := filepath.Join(t.TempDir(), "restart.db")
	if err := l.Service.Store.Backup(path); err != nil {
		t.Fatal(err)
	}
	store, err := control.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	l.Service.Store = store
	ask(t, l, plan)
	if err = l.Pass(context.Background(), instant); err == nil {
		t.Fatal("failed edit disappeared")
	}
	if len(f.calls["sendMessage"]) != 4 {
		t.Fatal("restart blocked later question")
	}
}

func TestFailedCallbackEditsDoNotBlockLaterCallbacks(t *testing.T) {
	l, f, plan := testLoop(t)
	first := ask(t, l, plan)
	second := ask(t, l, plan)
	pass(t, l, instant)
	f.setFailures("editMessageText", 1000)
	c1, c2 := callback(first, 11, 99, "first-callback"), callback(second, 12, 99, "second-callback")
	f.updates = []Incoming{{ID: 700, Callback: &c1}, {ID: 701, Callback: &c2}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	acknowledgements := 0
	f.onCall = func(method string, _ map[string]any) {
		if method == "answerCallbackQuery" {
			acknowledgements++
			if acknowledgements == 2 {
				cancel()
			}
		}
	}
	l.Run(ctx)
	rows, offset, _, err := deliveries(l.Service.Store)
	if err != nil || offset != 702 || len(rows) != 2 {
		t.Fatalf("state %+v %d %v", rows, offset, err)
	}
	for _, d := range rows {
		if d.Settled || d.Status != "Answered: yes" {
			t.Fatalf("lost presentation intent %+v", d)
		}
	}
	if out := command(t, l.Service, control.Request{Op: "inbox"}); len(out.Updates) != 0 {
		t.Fatal("later callback did not resolve")
	}
	f.setFailures("editMessageText", 0)
	pass(t, l, instant)
	rows, _, _, err = deliveries(l.Service.Store)
	if err != nil || !rows[0].Settled || !rows[1].Settled {
		t.Fatalf("presentation did not retry %+v %v", rows, err)
	}
}
