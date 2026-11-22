// Package telegram delivers the operator inbox and accepts choice callbacks.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Bot uses the outbound Bot API. BaseURL and HTTP are replaced by httptest.
type Bot struct {
	Token, BaseURL string
	HTTP           *http.Client
}

type apiError struct {
	Code      int
	Retry     time.Duration
	Unchanged bool
}

func (e *apiError) Error() string { return fmt.Sprintf("Telegram API error %d", e.Code) }

func (b Bot) call(ctx context.Context, method string, payload, result any) error {
	base := b.BaseURL
	if base == "" {
		base = "https://api.telegram.org"
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", base+"/bot"+b.Token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return errors.New("invalid Telegram endpoint")
	}
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{Timeout: 30 * time.Second}
	if b.HTTP != nil {
		client = *b.HTTP
		if client.Timeout == 0 {
			client.Timeout = 30 * time.Second
		}
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	// net/http errors contain the request URL, which contains the token.
	if err != nil {
		return errors.New("telegram request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	var envelope struct {
		OK          bool            `json:"ok"`
		Code        int             `json:"error_code"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
		Parameters  struct {
			Retry int `json:"retry_after"`
		} `json:"parameters"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&envelope); err != nil {
		return errors.New("invalid Telegram response")
	}
	if resp.StatusCode != 200 || !envelope.OK {
		return &apiError{Code: max(resp.StatusCode, envelope.Code), Retry: time.Duration(envelope.Parameters.Retry) * time.Second, Unchanged: strings.HasPrefix(envelope.Description, "Bad Request: message is not modified")}
	}
	if result != nil {
		if err = json.Unmarshal(envelope.Result, result); err != nil {
			return errors.New("invalid Telegram result")
		}
	}
	return nil
}

// Incoming contains only the message and callback fields engine uses.
type Incoming struct {
	ID       int64     `json:"update_id"`
	Message  *Message  `json:"message"`
	Callback *Callback `json:"callback_query"`
}

// Message identifies a chat and a bot message; From is shown during setup.
type Message struct {
	ID   int64 `json:"message_id"`
	Chat struct {
		ID   int64  `json:"id"`
		Type string `json:"type"`
	} `json:"chat"`
	From struct {
		ID       int64  `json:"id"`
		Name     string `json:"first_name"`
		Last     string `json:"last_name"`
		Username string `json:"username"`
	} `json:"from"`
	Text string `json:"text"`
}

// Callback is a button tap, bound to the original delivered message.
type Callback struct {
	ID      string   `json:"id"`
	Data    string   `json:"data"`
	Message *Message `json:"message"`
}

// Updates long-polls, confirming only updates below the durable offset.
func (b Bot) Updates(ctx context.Context, offset int64, timeout int) ([]Incoming, error) {
	var updates []Incoming
	err := b.call(ctx, "getUpdates", map[string]any{"offset": offset, "timeout": timeout, "allowed_updates": []string{"message", "callback_query"}}, &updates)
	return updates, err
}

// Send sends text to chat as an audible message with no buttons, for a caller
// that never polls the bot.
func (b Bot) Send(ctx context.Context, chat int64, text string) error {
	var msg Message
	err := b.call(ctx, "sendMessage", map[string]any{"chat_id": chat, "text": text}, &msg)
	if err == nil && msg.ID == 0 {
		err = errors.New("telegram send returned no message ID")
	}
	return err
}
