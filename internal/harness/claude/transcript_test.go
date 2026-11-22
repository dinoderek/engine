package claude

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dinoderek/engine/internal/harness"
)

func TestPromptsAreTheTranscriptsSubmittedPrompts(t *testing.T) {
	h := New(harness.Env{}).Hooks()
	got, err := h.Prompts(filepath.Join("testdata", "transcript.jsonl"))
	want := []harness.Prompt{
		{Text: "Use the engine skill's server executor mode for t7."},
		{Text: "<system-reminder>skill loaded</system-reminder>", Injected: true},
		{Text: "Yes, drop the column.\nKeep the index."},
		{Text: "<agent-message from=\"a1\">\n[Subagent hand-back] Verdict: APPROVED\n</agent-message>", Injected: true},
		{Text: "<task-notification>\n<task-id>b1</task-id>\n<status>completed</status>\n</task-notification>", Injected: true},
		{Text: "<task-notification>\n<task-id>b2</task-id>\n<status>completed</status>\n</task-notification>", Injected: true},
		{Text: "Also keep the old name."},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("prompts %+v, %v;\nwant %+v: no tool result or tool call; a meta, peer or task-notification turn, or queued command, injected; a hand-back without its frame; a person's queued command typed", got, err, want)
	}
	if _, err := h.Prompts(filepath.Join(t.TempDir(), "missing.jsonl")); err == nil {
		t.Error("read a missing transcript")
	}
}

func TestAPromptEventCarriesItsTextAndTranscript(t *testing.T) {
	ev, err := New(harness.Env{}).Hooks().Event([]byte(claudeEvent("UserPromptSubmit", `,"prompt":"Yes, drop it"`)))
	if err != nil || ev.Prompt != "Yes, drop it" || ev.Transcript != "/home/u/.claude/projects/p/"+claudeSession+".jsonl" {
		t.Errorf("event %+v, %v", ev, err)
	}
}

func TestAPeersTurnIsItsAgentMessage(t *testing.T) {
	for _, tc := range []struct{ name, text, want string }{
		{"framed", "Another Claude session sent a message:\n<agent-message from=\"a\">\nhi\n</agent-message>\n\nA note.", "<agent-message from=\"a\">\nhi\n</agent-message>"},
		{"bare", "<agent-message>hi</agent-message>", "<agent-message>hi</agent-message>"},
		{"none", "<system-reminder>x</system-reminder>", "<system-reminder>x</system-reminder>"},
		{"unclosed", "note\n<agent-message>hi", "note\n<agent-message>hi"},
		{"closed before opened", "</agent-message> then <agent-message>", "</agent-message> then <agent-message>"},
	} {
		if got := agentMessage(tc.text); got != tc.want {
			t.Errorf("%s: agentMessage(%q) = %q, want %q", tc.name, tc.text, got, tc.want)
		}
	}
}
