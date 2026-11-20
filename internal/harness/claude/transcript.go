package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
)

// Prompts are the prompts in a Claude Code transcript: its user records
// that are neither meta nor tool results, whose content is the submitted
// text or its text blocks.
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

// userPrompt is the prompt a transcript line records, if it records one.
func userPrompt(line []byte) (string, bool) {
	if !bytes.Contains(line, []byte(`"user"`)) {
		return "", false
	}
	var rec struct {
		Type    string `json:"type"`
		Meta    bool   `json:"isMeta"`
		Message struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &rec) != nil || rec.Type != "user" || rec.Meta || rec.Message.Role != "user" {
		return "", false
	}
	var text string
	if json.Unmarshal(rec.Message.Content, &text) == nil {
		return text, true
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(rec.Message.Content, &blocks) != nil {
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
