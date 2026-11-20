package codex

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
)

// Prompts are the prompts in a Codex rollout: its user messages, as
// response items of input text or as user-message events.
func (hooks) Prompts(transcript string) ([]string, error) {
	f, err := os.Open(transcript)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var prompts []string
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if p, ok := userMessage(line); ok {
			prompts = append(prompts, p)
		}
		if errors.Is(err, io.EOF) {
			return prompts, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

// userMessage is the prompt a rollout line records, if it records one.
func userMessage(line []byte) (string, bool) {
	if !bytes.Contains(line, []byte(`"user`)) {
		return "", false
	}
	var rec struct {
		Type    string `json:"type"`
		Payload struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Message string `json:"message"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &rec) != nil {
		return "", false
	}
	switch {
	case rec.Type == "event_msg" && rec.Payload.Type == "user_message":
		return rec.Payload.Message, true
	case rec.Type == "response_item" && rec.Payload.Type == "message" && rec.Payload.Role == "user":
		var parts []string
		for _, c := range rec.Payload.Content {
			if c.Type == "input_text" {
				parts = append(parts, c.Text)
			}
		}
		return strings.Join(parts, "\n"), len(parts) > 0
	}
	return "", false
}
