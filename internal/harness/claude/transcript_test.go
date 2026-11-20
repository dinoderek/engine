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
	want := []string{"Use the engine skill's server executor mode for t7.", "Yes, drop the column.\nKeep the index."}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("prompts %q, %v; want %q: no meta record, tool result or tool call", got, err, want)
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
