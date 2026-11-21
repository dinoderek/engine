package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/dinoderek/engine/internal/harness"
)

// Prompts are the prompts in a Claude Code transcript: its user records
// that are not tool results, whose content is the submitted text or its
// text blocks. A meta record, or one whose origin is not a person, such as
// a subagent's hand-back or a task notification, is injected.
func (hooks) Prompts(transcript string) ([]harness.Prompt, error) {
	f, err := os.Open(transcript)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var prompts []harness.Prompt
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if p, ok := userPrompt(line); ok {
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

// humanOrigin is the origin kind of a turn a person submitted; a record
// with no origin predates the field and is a person's too.
const humanOrigin = "human"

// userPrompt is the prompt a transcript line records, if it records one.
func userPrompt(line []byte) (harness.Prompt, bool) {
	if !bytes.Contains(line, []byte(`"user"`)) {
		return harness.Prompt{}, false
	}
	var rec struct {
		Type   string `json:"type"`
		Meta   bool   `json:"isMeta"`
		Origin struct {
			Kind string `json:"kind"`
		} `json:"origin"`
		Message struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &rec) != nil || rec.Type != "user" || rec.Message.Role != "user" {
		return harness.Prompt{}, false
	}
	text, ok := promptText(rec.Message.Content)
	injected := rec.Meta || (rec.Origin.Kind != "" && rec.Origin.Kind != humanOrigin)
	return harness.Prompt{Text: text, Injected: injected}, ok
}

// promptText is the text of a user record's content: a string, or its text
// blocks; a tool result is no prompt.
func promptText(content json.RawMessage) (string, bool) {
	var text string
	if json.Unmarshal(content, &text) == nil {
		return text, true
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &blocks) != nil {
		return "", false
	}
	var parts []string
	for _, b := range blocks {
		switch b.Type {
		case "tool_result":
			return "", false
		case "text":
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n"), len(parts) > 0
}
