package codex

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dinoderek/engine/internal/harness"
)

func TestPromptsAreTheRolloutsUserMessages(t *testing.T) {
	h := New(harness.Env{}, Options{}).Hooks()
	got, err := h.Prompts(filepath.Join("testdata", "rollout.jsonl"))
	want := []harness.Prompt{{Text: "Use the engine skill's server executor mode for t7."}, {Text: "Yes, drop the column."}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("prompts %+v, %v; want %+v: no developer, assistant or tool message", got, err, want)
	}
	if _, err := h.Prompts(filepath.Join(t.TempDir(), "missing.jsonl")); err == nil {
		t.Error("read a missing rollout")
	}
}

func TestAPromptEventCarriesItsTextAndTranscript(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "hooks", "UserPromptSubmit.json"))
	if err != nil {
		t.Fatal(err)
	}
	ev, err := New(harness.Env{}, Options{}).Hooks().Event(raw)
	if err != nil || ev.Prompt == "" || filepath.Ext(ev.Transcript) != ".jsonl" {
		t.Errorf("event %+v, %v; want the prompt and its rollout", ev, err)
	}
}
