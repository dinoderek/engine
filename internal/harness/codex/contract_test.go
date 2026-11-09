package codex

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/harness/harnesstest"
)

// recordedKinds are the events recorded from a live Codex session, in
// testdata/hooks, with their kinds.
var recordedKinds = map[string]harness.EventKind{
	"SessionStart": harness.SessionStarted, "UserPromptSubmit": harness.PromptSubmitted, "PreToolUse": harness.ToolStarting,
	"PostToolUse": harness.ToolFinished, "PermissionRequest": harness.PermissionRequested, "Stop": harness.TurnStopped,
	"Interrupt": harness.Interrupted, "SessionEnd": harness.SessionEnded,
}

// recorded reads the event called name, recorded from a live Codex session.
// The paths in it are scrubbed; the fields are as Codex sent them.
func recorded(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "hooks", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestContract(t *testing.T) {
	var fx harnesstest.Fixtures
	for name, kind := range recordedKinds {
		fx.Events = append(fx.Events, harnesstest.Event{Raw: recorded(t, name), Name: name, Kind: kind})
	}
	down := errors.New("no app-server")
	harnesstest.Contract(t, func(env harness.Env) harness.Adapter {
		// engine setup makes engine's Codex home, which a joined session's
		// trust edits.
		if err := os.MkdirAll(EngineHome(env.Home), 0o700); err != nil {
			t.Fatal(err)
		}
		return New(env, Options{
			Quota: func(context.Context, string, string) (json.RawMessage, error) { return nil, down },
			Turns: func(context.Context, string, string, []string) (map[string]json.RawMessage, error) { return nil, down },
		})
	}, fx)
}
