package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBotFailuresDoNotExposeTokenAndRedirectsAreRefused(t *testing.T) {
	for _, body := range []string{`{"ok":false,"error_code":429,"parameters":{"retry_after":90}}`, `not json`, `{"ok":true,"result":"bad updates"}`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, body) }))
			defer server.Close()
			b := Bot{Token: "SECRET", BaseURL: server.URL, HTTP: server.Client()}
			_, err := b.Updates(context.Background(), 0, 0)
			if err == nil || strings.Contains(err.Error(), "SECRET") {
				t.Fatal(err)
			}
			if strings.Contains(body, "429") {
				var api *apiError
				if !errors.As(err, &api) || api.Retry != 90*time.Second {
					t.Fatal(err)
				}
			}
		})
	}
	redirected := false
	dest := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected = true }))
	defer dest.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dest.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	b := Bot{Token: "SECRET", BaseURL: source.URL, HTTP: source.Client()}
	if _, err := b.Updates(context.Background(), 0, 0); err == nil || redirected {
		t.Fatalf("redirect %t error %v", redirected, err)
	}
	source.Close()
	if _, err := b.Updates(context.Background(), 0, 0); err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatal(err)
	}
	b.BaseURL = ":bad"
	if _, err := b.Updates(context.Background(), 0, 0); err == nil {
		t.Fatal("invalid endpoint")
	}
}

func TestEditAlreadyAppliedIsSuccessful(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"ok":false,"error_code":400,"description":"Bad Request: message is not modified: specified new message content and reply markup are exactly the same as a current content and reply markup of the message"}`)
	}))
	defer server.Close()
	l := Loop{Bot: Bot{BaseURL: server.URL, HTTP: server.Client()}, Chat: 1}
	if err := l.edit(context.Background(), 2, "same"); err != nil {
		t.Fatal(err)
	}
}

func TestSendPostsPlainTextToTheChat(t *testing.T) {
	var got map[string]any
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = fmt.Fprint(w, `{"ok":true,"result":{"message_id":7}}`)
	}))
	defer server.Close()
	bot := Bot{Token: "123:secret", BaseURL: server.URL}
	if err := bot.Send(context.Background(), 42, "down"); err != nil {
		t.Fatal(err)
	}
	if path != "/bot123:secret/sendMessage" || got["chat_id"] != float64(42) || got["text"] != "down" || len(got) != 2 {
		t.Errorf("sent %s %v", path, got)
	}
}

func TestSendWithoutAMessageIDFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"ok":true,"result":{}}`)
	}))
	defer server.Close()
	if err := (Bot{Token: "t", BaseURL: server.URL}).Send(context.Background(), 1, "x"); err == nil {
		t.Error("a send with no message ID succeeded")
	}
}
