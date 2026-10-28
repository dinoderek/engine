package hostops

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/session"
)

const failedCodexTurn = `{"data":[{"id":"turn-a","status":"failed","startedAt":900,"completedAt":1000,"error":{"codexErrorInfo":"usageLimitExceeded"}}]}`

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

func TestStatusSeesCodexFailureWithoutAStopHook(t *testing.T) {
	for _, tc := range []struct {
		name, quota, turn, state string
		reset                    int64
		unavailable              bool
	}{
		{"working becomes limited", "both", "turn-a", "limited", 3000, false},
		{"expired quota keeps limit", "expired", "turn-a", "limited", 0, false},
		{"unknown quota keeps limit", "failed", "turn-a", "limited", 0, false},
		{"later prompt wins", "both", "turn-b", "working", 0, false},
		{"unavailable history", "both", "turn-a", "working", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHome(t, "mac")
			at := time.Unix(1000, 0)
			dir := writeRun(t, h, session.Record{RunID: "run-a", Name: "r-1", Harness: Codex, Spawned: time.Unix(800, 0)})
			st := session.HarnessState{State: session.StateWorking, Event: "PostToolUse", At: at.Add(-time.Millisecond), SessionID: "thread-a", TurnID: tc.turn}
			if err := session.WriteHarnessState(dir, st); err != nil {
				t.Fatal(err)
			}
			tm := &tmuxFake{windows: []string{"@1 %1 0 r-1"}, screens: map[string]string{"@1": "usage limit"}}
			d := Deps{Home: h.dir, Tmux: tm.tmux(), Exec: agentsFake("[]", nil), Now: func() time.Time { return at }}
			d.CodexQuota = func(context.Context, string, string) (json.RawMessage, error) { return codexLimitQuota(tc.quota) }
			d.CodexTurns = func(ctx context.Context, bin, home string, ids []string) (map[string]json.RawMessage, error) {
				if _, ok := ctx.Deadline(); !ok || bin != filepath.Join(h.dir, "bin/codex") || len(ids) != 1 || ids[0] != "thread-a" {
					t.Fatal("incorrect bounded request")
				}
				if tc.unavailable {
					return nil, errors.New("unavailable")
				}
				if home != filepath.Join(h.dir, ".local/share/engine/codex") {
					t.Fatal("incorrect engine home")
				}
				return map[string]json.RawMessage{"thread-a": json.RawMessage(failedCodexTurn)}, nil
			}
			got, err := ReadStatus(d)
			if err != nil || got.Windows[0].State != tc.state || got.Windows[0].ResetsAt != tc.reset {
				t.Fatalf("status %+v, %v", got, err)
			}
			tm.readOnly(t)
		})
	}
}

func TestCodexTurnReadingsTryTheEmbeddedHome(t *testing.T) {
	h := newHome(t, "mac")
	at := fixedNow()
	dir := writeRun(t, h, session.Record{RunID: "r-a", Name: "r-1", Harness: Codex, Spawned: at})
	if err := session.WriteHarnessState(dir, session.HarnessState{State: session.StateWorking, SessionID: "a", At: at}); err != nil {
		t.Fatal(err)
	}
	_, host, err := openHost(h.dir)
	if err != nil {
		t.Fatal(err)
	}
	var homes []string
	d := Deps{Home: h.dir, CodexTurns: func(_ context.Context, _, home string, ids []string) (map[string]json.RawMessage, error) {
		homes = append(homes, home)
		if len(homes) == 1 {
			return nil, nil
		}
		return map[string]json.RawMessage{ids[0]: json.RawMessage(failedCodexTurn)}, nil
	}}
	runs, err := session.LatestRuns(h.dir)
	if err != nil {
		t.Fatal(err)
	}
	got := codexTurnReadings(d, host, []session.WindowInfo{{Name: "r-1"}}, runs)
	if len(homes) != 2 || homes[1] != filepath.Join(h.dir, ".codex") || string(got["a"]) != failedCodexTurn {
		t.Fatalf("homes %v, turns %s", homes, got["a"])
	}
}
