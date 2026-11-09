package hostops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/harness/codex"
	"github.com/dinoderek/engine/internal/session"
)

const failedCodexTurn = `{"data":[{"id":"turn-a","status":"failed","startedAt":900,"completedAt":1000,"error":{"codexErrorInfo":"usageLimitExceeded"}}]}`

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
			quota := func(context.Context, string, string) (json.RawMessage, error) { return codexLimitQuota(tc.quota) }
			turns := func(ctx context.Context, bin, home string, ids []string) (map[string]json.RawMessage, error) {
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
			got, err := ReadStatus(withCodex(d, codex.Options{Quota: quota, Turns: turns}))
			if err != nil || got.Windows[0].State != tc.state || got.Windows[0].ResetsAt != tc.reset {
				t.Fatalf("status %+v, %v", got, err)
			}
			tm.readOnly(t)
		})
	}
}

func TestCodexHistoryReadsOnlyLiveEngineThreads(t *testing.T) {
	h := newHome(t, "mac")
	at := fixedNow()
	var windows []string
	for i, tc := range []struct {
		name, harness, state, id string
		dead                     bool
	}{
		{"active", Codex, session.StateWorking, "active-id", false},
		{"dead", Codex, session.StateWorking, "dead-id", true},
		{"ended", Codex, session.StateEnded, "ended-id", false},
		{"claude", Claude, session.StateWorking, "claude-id", false},
		{"missing", Codex, "", "", false},
		{"no-id", Codex, session.StateWorking, "", false},
	} {
		dir := writeRun(t, h, session.Record{RunID: tc.name, Name: tc.name, Harness: tc.harness, Spawned: at})
		dead := "0"
		if tc.dead {
			dead = "1"
		}
		windows = append(windows, fmt.Sprintf("@%d %%%d %s %s", i+1, i+1, dead, tc.name))
		if tc.state != "" {
			if err := session.WriteHarnessState(dir, session.HarnessState{State: tc.state, SessionID: tc.id, At: at}); err != nil {
				t.Fatal(err)
			}
		}
	}
	windows = append(windows, "@9 %9 0 no-run")
	calls := 0
	d := withCodex(Deps{Home: h.dir, Tmux: (&tmuxFake{windows: windows}).tmux(), Exec: agentsFake("[]", nil), Now: fixedNow}, codex.Options{
		Quota: func(context.Context, string, string) (json.RawMessage, error) { return nil, errors.New("no quota") },
		Turns: func(_ context.Context, _, _ string, ids []string) (map[string]json.RawMessage, error) {
			calls++
			if len(ids) != 1 || ids[0] != "active-id" {
				t.Fatalf("read unrelated threads: %v", ids)
			}
			return map[string]json.RawMessage{"active-id": json.RawMessage(failedCodexTurn), "unasked-id": json.RawMessage(failedCodexTurn)}, nil
		},
	})
	got, err := ReadStatus(d)
	if err != nil || calls != 1 || got.Windows[0].State != session.StateWorking {
		t.Fatalf("calls %d, status %+v, %v", calls, got, err)
	}
}
