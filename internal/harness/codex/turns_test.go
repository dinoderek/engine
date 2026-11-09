package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/session"
)

func TestFailedCodexTurnOverridesOnlyOlderHooks(t *testing.T) {
	for _, tc := range []struct {
		name, body, turn string
		at               int64
		want             bool
	}{
		{"failure after tool", failedCodexTurn, "turn-a", 999, true},
		{"same second tool", failedCodexTurn, "turn-a", 1000, true},
		{"legacy hook", failedCodexTurn, "", 999, true},
		{"later hook", failedCodexTurn, "turn-a", 1001, false},
		{"newer failed turn", failedCodexTurn, "prior-turn", 899, true},
		{"new prompt same second", failedCodexTurn, "turn-b", 1000, false},
		{"new in-flight turn", `{"data":[{"id":"turn-b","status":"interrupted","startedAt":1001,"completedAt":null},` + strings.TrimPrefix(failedCodexTurn, `{"data":[`), "turn-b", 1001, false},
		{"completed", strings.Replace(failedCodexTurn, `"failed"`, `"completed"`, 1), "turn-a", 999, false},
		{"other error", strings.Replace(failedCodexTurn, "usageLimitExceeded", "unauthorized", 1), "turn-a", 999, false},
		{"missing completion", strings.Replace(failedCodexTurn, `"completedAt":1000`, `"completedAt":null`, 1), "turn-a", 999, false},
		{"bad times", strings.Replace(failedCodexTurn, `"startedAt":900`, `"startedAt":1001`, 1), "turn-a", 999, false},
		{"malformed", "{", "turn-a", 999, false},
		{"empty", `{"data":[]}`, "turn-a", 999, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := session.HarnessState{At: time.Unix(tc.at, 600000000), TurnID: tc.turn}
			if got := codexTurnFailed(json.RawMessage(tc.body), st); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
	if codexTurnFailed(json.RawMessage(failedCodexTurn), session.HarnessState{}) {
		t.Fatal("failure with no hook timestamp")
	}
}

func TestCodexTurnReadingsTryTheEmbeddedHome(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := session.WriteHarnessState(dir, session.HarnessState{State: session.StateWorking, SessionID: "a", At: at}); err != nil {
		t.Fatal(err)
	}
	var homes []string
	a := New(harness.Env{Home: home}, Options{Turns: func(_ context.Context, _, home string, ids []string) (map[string]json.RawMessage, error) {
		homes = append(homes, home)
		if len(homes) == 1 {
			return nil, nil
		}
		return map[string]json.RawMessage{ids[0]: json.RawMessage(failedCodexTurn)}, nil
	}})
	got := a.turns(context.Background(), "codex", map[string]session.Run{"r-1": {Dir: dir}})
	if len(homes) != 2 || homes[0] != filepath.Join(home, ".local/share/engine/codex") || homes[1] != filepath.Join(home, ".codex") || string(got["a"]) != failedCodexTurn {
		t.Fatalf("homes %v, turns %s", homes, got["a"])
	}
}

func TestRecordedCodexUsageFailure(t *testing.T) {
	data, err := os.ReadFile("testdata/codex-usage-limit.json")
	if err != nil {
		t.Fatal(err)
	}
	at, err := time.Parse(time.RFC3339Nano, "2026-10-27T13:33:11.668825Z")
	if err != nil {
		t.Fatal(err)
	}
	st := session.HarnessState{State: session.StateWorking, Event: "PostToolUse", At: at}
	if !codexStateLimited(data, st, time.Unix(1793107602, 0)) {
		t.Fatal("recorded usage failure remained working")
	}
	st.At = time.Unix(1793107992, 0)
	if codexStateLimited(data, st, time.Unix(1793107602, 0)) {
		t.Fatal("recorded old failure overrode newer work")
	}
}
