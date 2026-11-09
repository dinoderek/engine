package codex

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/harness/harnesstest"
)

func TestContract(t *testing.T) {
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
	})
}
