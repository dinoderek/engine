package hostops

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/harness/codex"
	"github.com/dinoderek/engine/internal/session"
)

func TestStatusCodexQuota(t *testing.T) {
	for _, kind := range []string{"success", "failed", "timeout", "partial", "invalid"} {
		t.Run(kind, func(t *testing.T) {
			h := newHome(t, "mac")
			at := fixedNow()
			// A failed live call must not revive a persisted Codex observation.
			dir := writeRun(t, h, session.Record{RunID: "old", Name: "old", Harness: Codex, Spawned: at})
			write(t, filepath.Join(dir, session.QuotaFile), `{"harness":"codex","observed_at":1,"windows":{"five_hour":{"used":99,"resets_at":9999999999}}}`, 0600)
			called := false
			d := Deps{Home: h.dir, Tmux: (&tmuxFake{}).tmux(), Exec: agentsFake("[]", nil), Now: func() time.Time { return at }, Getenv: func(string) string { return "/operator" }}
			quota := func(ctx context.Context, bin, home string) (json.RawMessage, error) {
				called = true
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("missing deadline")
				}
				if bin != filepath.Join(h.dir, "bin", "codex") || home != filepath.Join(h.dir, ".local/share/engine/codex") {
					t.Fatalf("wrong bin or home: %s %s", bin, home)
				}
				switch kind {
				case "failed":
					return nil, errors.New("not signed in")
				case "timeout":
					return nil, context.DeadlineExceeded
				case "partial":
					return json.RawMessage(`{"rateLimits":{"secondary":{"usedPercent":41,"resetsAt":9999999999}}}`), nil
				case "invalid":
					return json.RawMessage(`{`), nil
				default:
					return json.RawMessage(`{"rateLimits":{"primary":{"usedPercent":83,"resetsAt":1},"secondary":{"usedPercent":41,"resetsAt":9999999999}}}`), nil
				}
			}
			got, err := ReadStatus(withCodex(d, codex.Options{Quota: quota}))
			if err != nil || !called {
				t.Fatalf("status %+v, %v, called %v", got, err, called)
			}
			assertStatusCodexQuota(t, kind, got.Quota[Codex], at)
		})
	}
	h := newHome(t, "cl")
	_, err := ReadStatus(withCodex(Deps{Home: h.dir, Tmux: (&tmuxFake{}).tmux(), Exec: agentsFake("[]", nil)}, codex.Options{Quota: func(context.Context, string, string) (json.RawMessage, error) {
		t.Fatal("Claude-only host asked Codex")
		return nil, nil
	}}))
	if err != nil {
		t.Fatal(err)
	}
}

func assertStatusCodexQuota(t *testing.T, kind string, q session.Quota, at time.Time) {
	t.Helper()
	if kind != "success" && kind != "partial" {
		if q.Harness != "" {
			t.Fatalf("failed call kept quota %+v", q)
		}
		return
	}
	if q.Harness != Codex || q.ObservedAt != at.Unix() || q.Windows["seven_day"].Used != 41 {
		t.Fatalf("quota %+v", q)
	}
	if kind == "success" && q.Windows["five_hour"].Used != 0 {
		t.Fatal("expired window not cleared")
	}
	if kind == "partial" && len(q.Windows) != 1 {
		t.Fatalf("invented window %+v", q)
	}
}
