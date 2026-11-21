package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/telegram"
)

// fakeTelegram is the Bot API on httptest: it records every call, numbers
// the messages it accepts, and hands the loop's long poll the updates a test
// queues, as a phone's taps reach Telegram.
type fakeTelegram struct {
	t      *testing.T
	server *httptest.Server

	mu      sync.Mutex
	calls   []botCall
	queued  []map[string]any
	nextMsg int64
	nextUpd int64
}

// botCall is one Bot API request: its method and JSON body.
type botCall struct {
	Method string
	Body   map[string]any
}

func newFakeTelegram(t *testing.T) *fakeTelegram {
	t.Helper()
	f := &fakeTelegram{t: t, nextMsg: 100, nextUpd: 1}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeTelegram) serve(w http.ResponseWriter, r *http.Request) {
	method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		f.t.Errorf("Bot API %s: %v", method, err)
	}
	f.mu.Lock()
	f.calls = append(f.calls, botCall{method, body})
	var result any = true
	switch method {
	case "sendMessage":
		f.nextMsg++
		result = map[string]any{"message_id": f.nextMsg, "chat": map[string]any{"id": body["chat_id"], "type": "private"}}
	case "getUpdates":
		updates := append([]map[string]any{}, f.queued...)
		f.queued, result = nil, updates
	}
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
}

// loop runs the Telegram loop engine serve runs, on the server's own
// service, against this fake, until the returned stop is called.
func (f *fakeTelegram) loop(service control.Service, chat int64, dashboard string) (stop func()) {
	l := &telegram.Loop{Service: service, Chat: chat, Dashboard: dashboard, Logf: f.t.Logf,
		Bot: telegram.Bot{Token: strings.Repeat("a", 40), BaseURL: f.server.URL, HTTP: f.server.Client()}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); l.Run(ctx) }()
	var once sync.Once
	stop = func() { once.Do(func() { cancel(); <-done }) }
	f.t.Cleanup(stop)
	return stop
}

// tap queues a button tap on a bot message, from chat, as Telegram delivers
// it; a redelivery reuses the callback's ID under a new update ID.
func (f *fakeTelegram) tap(callback string, chat, message int64, data string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextUpd++
	f.queued = append(f.queued, map[string]any{"update_id": f.nextUpd, "callback_query": map[string]any{
		"id": callback, "data": data,
		"message": map[string]any{"message_id": message, "chat": map[string]any{"id": chat, "type": "private"}}}})
}

// sent is every call of method so far, in order.
func (f *fakeTelegram) sent(method string) []botCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []botCall
	for _, c := range f.calls {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

// message is the ID the fake gave the nth sendMessage, from 0.
func (f *fakeTelegram) message(n int) int64 { return 101 + int64(n) }

// edits is the text of every edit of message, in order.
func (f *fakeTelegram) edits(message int64) []string {
	var out []string
	for _, c := range f.sent("editMessageText") {
		if id, _ := c.Body["message_id"].(float64); int64(id) == message {
			text, _ := c.Body["text"].(string)
			out = append(out, text)
		}
	}
	return out
}
