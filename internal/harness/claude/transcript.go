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
// text blocks, and its queued commands, which the harness delivers between
// turns. A meta record, or one whose origin is not a person, such as a
// subagent's hand-back or a task notification, is injected; a hand-back's
// text is its agent-message, without the frame the harness wraps it in.
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
		if p, ok := prompt(line); ok {
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

// origin is who a transcript record says submitted its turn.
type origin struct {
	Kind string `json:"kind"`
}

func (o origin) human() bool { return o.Kind == "" || o.Kind == humanOrigin }

// record is the part of a transcript line a prompt is read from.
type record struct {
	Type    string `json:"type"`
	Meta    bool   `json:"isMeta"`
	Origin  origin `json:"origin"`
	Message struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	Attachment struct {
		Type   string `json:"type"`
		Prompt string `json:"prompt"`
		Origin origin `json:"origin"`
	} `json:"attachment"`
}

// prompt is the prompt a transcript line records, if it records one.
func prompt(line []byte) (harness.Prompt, bool) {
	if !bytes.Contains(line, []byte(`"user"`)) && !bytes.Contains(line, []byte(`"queued_command"`)) {
		return harness.Prompt{}, false
	}
	var rec record
	if json.Unmarshal(line, &rec) != nil {
		return harness.Prompt{}, false
	}
	switch {
	case rec.Type == "attachment" && rec.Attachment.Type == "queued_command" && rec.Attachment.Prompt != "":
		return harness.Prompt{Text: rec.Attachment.Prompt, Injected: !rec.Attachment.Origin.human()}, true
	case rec.Type != "user" || rec.Message.Role != "user":
		return harness.Prompt{}, false
	}
	text, ok := promptText(rec.Message.Content)
	injected := rec.Meta || !rec.Origin.human()
	if injected {
		text = agentMessage(text)
	}
	return harness.Prompt{Text: text, Injected: injected}, ok
}

// agentMessage is the agent-message element of a peer's turn, which the
// harness frames with notes of its own, or text when it holds none.
func agentMessage(text string) string {
	const open, end = "<agent-message", "</agent-message>"
	i, j := strings.Index(text, open), strings.LastIndex(text, end)
	if i < 0 || j < i {
		return text
	}
	return text[i : j+len(end)]
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
