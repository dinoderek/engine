package hostops

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/dinoderek/engine/internal/session"
)

func TestClaudeLimitStatus(t *testing.T) {
	for _, tc := range []struct {
		name, agents, marker, state string
		dead                        bool
		err                         error
	}{
		{"waiting", `[{"name":"r-1","status":"waiting"}]`, "valid", session.StateLimited, false, nil},
		{"idle", `[{"name":"r-1","status":"idle"}]`, "valid", session.StateLimited, false, nil},
		{"not listed", `[]`, "valid", session.StateLimited, false, nil},
		{"busy wins", `[{"name":"r-1","status":"busy"}]`, "valid", session.StateWorking, false, nil},
		{"dead wins", `[{"name":"r-1","status":"busy"}]`, "valid", "", true, nil},
		{"no marker", `[{"name":"r-1","status":"waiting"}]`, "", session.StateWaiting, false, nil},
		{"corrupt marker", `[{"name":"r-1","status":"waiting"}]`, "{", session.StateWaiting, false, nil},
		{"probe failed", "", "valid", "", false, errors.New("unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHome(t, "mac")
			dir := writeRun(t, h, session.Record{RunID: "r-1-a", Name: "r-1", Harness: Claude, Spawned: fixedNow()})
			body := tc.marker
			if body == "valid" {
				body = `{"since":1000,"resets_at":2000,"error":"rate_limit"}`
			}
			if body != "" {
				write(t, filepath.Join(dir, session.LimitFile), body, 0o600)
			}
			line := "@1 %1 0 r-1"
			if tc.dead {
				line = "@1 %1 1 r-1"
			}
			tm := &tmuxFake{windows: []string{line}, screens: map[string]string{"@1": "usage limit"}}
			st, err := ReadStatus(Deps{Home: h.dir, Tmux: tm.tmux(), Exec: agentsFake(tc.agents, tc.err)})
			if err != nil {
				t.Fatal(err)
			}
			w := st.Windows[0]
			if w.State != tc.state || w.Alive == tc.dead {
				t.Fatalf("window %+v", w)
			}
			wantReset := int64(0)
			if tc.state == session.StateLimited {
				wantReset = 2000
			}
			if w.ResetsAt != wantReset {
				t.Errorf("reset %+v", w)
			}

			tm.readOnly(t)
		})
	}
}
