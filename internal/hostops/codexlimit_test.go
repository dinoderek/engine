package hostops

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/session"
)

func TestCodexLimitStatus(t *testing.T) {
	for _, tc := range []struct {
		name, state, quota, want string
		reset                    int64
		dead                     bool
	}{
		{"exhausted primary", "waiting", "primary", "limited", 2000, false},
		{"exhausted secondary", "waiting", "secondary", "limited", 3000, false},
		{"both exhausted", "waiting", "both", "limited", 3000, false},
		{"working wins", "working", "both", "working", 0, false},
		{"below limit", "waiting", "below", "waiting", 0, false},
		{"reset passed", "waiting", "expired", "waiting", 0, false},
		{"unknown quota", "waiting", "failed", "waiting", 0, false},
		{"partial quota", "waiting", "partial", "waiting", 0, false},
		{"missing state", "", "both", "", 0, false},
		{"corrupt state", "corrupt", "both", "", 0, false},
		{"ended", "ended", "both", "", 0, false},
		{"dead", "waiting", "both", "", 0, true},
		{"hook wins", "limited", "both", "limited", 4000, false},
		{"hook without quota", "limited", "failed", "limited", 4000, false},
		{"dead hook", "limited", "both", "", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHome(t, "mac")
			at := time.Unix(1000, 0)
			dir := writeRun(t, h, session.Record{RunID: "r-1-a", Name: "r-1", Harness: Codex, Spawned: at})
			if tc.state != "" {
				body := `{"state":"` + tc.state + `","resets_at":4000}`
				if tc.state == "corrupt" {
					body = "{"
				}
				write(t, filepath.Join(dir, session.StateFile), body, 0o600)
			}
			line := "@1 %1 0 r-1"
			if tc.dead {
				line = "@1 %1 1 r-1"
			}
			tm := &tmuxFake{windows: []string{line}, screens: map[string]string{"@1": "usage limit"}}
			d := Deps{Home: h.dir, Tmux: tm.tmux(), Exec: agentsFake("[]", nil), Now: func() time.Time { return at }}
			d.CodexQuota = func(context.Context, string, string) (json.RawMessage, error) {
				return codexLimitQuota(tc.quota)
			}
			st, err := ReadStatus(d)
			if err != nil {
				t.Fatal(err)
			}
			w := st.Windows[0]
			if w.State != tc.want || w.ResetsAt != tc.reset || w.Alive == tc.dead {
				t.Fatalf("window %+v; want %s, reset %d, dead %v", w, tc.want, tc.reset, tc.dead)
			}
			if (w.Screen == "") != (tc.want == session.StateWorking) {
				t.Errorf("unexpected screen %+v", w)
			}
			tm.readOnly(t)
		})
	}
}

func codexLimitQuota(kind string) (json.RawMessage, error) {
	primary, secondary := 99.0, 99.0
	reset := int64(2000)
	switch kind {
	case "primary":
		primary = 100
	case "secondary":
		secondary = 100
	case "both":
		primary, secondary = 100, 100
	case "expired":
		primary, reset = 100, 1000
	case "failed":
		return nil, errors.New("quota unavailable")
	case "partial":
		return json.RawMessage(`{"rateLimits":{"secondary":{"usedPercent":99,"resetsAt":3000}}}`), nil
	}
	return json.Marshal(map[string]any{"rateLimits": map[string]any{
		"primary":   map[string]any{"usedPercent": primary, "resetsAt": reset},
		"secondary": map[string]any{"usedPercent": secondary, "resetsAt": 3000},
	}})
}
