package hostops

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/dinoderek/engine/internal/session"
)

// A Claude session that Claude Code lists as busy, but whose last hook event
// was a Stop, is working only in a background job. Anything less certain
// leaves it busy in a turn, and a failed session list leaves it unknown.
func TestClaudeBackgroundStatus(t *testing.T) {
	const id = "11111111-1111-4111-8111-111111111111"
	busy := `[{"name":"r-1","sessionId":"` + id + `","status":"busy"}]`
	stop := `{"state":"waiting","event":"Stop","at":"2026-01-02T03:04:05Z","session_id":"` + id + `"}`
	for _, tc := range []struct {
		name, agents, file, state string
		err                       error
		background                bool
	}{
		{"busy after its turn ended", busy, stop, session.StateWorking, nil, true},
		{"busy after a Stop that names no session", busy, `{"state":"waiting","event":"Stop"}`, session.StateWorking, nil, true},
		{"busy in a turn: a tool call is running", busy, `{"state":"working","event":"PreToolUse","session_id":"` + id + `"}`, session.StateWorking, nil, false},
		{"busy in a turn: a prompt was submitted", busy, `{"state":"working","event":"UserPromptSubmit","session_id":"` + id + `"}`, session.StateWorking, nil, false},
		{"another session's Stop", busy, `{"state":"waiting","event":"Stop","session_id":"22222222-2222-4222-8222-222222222222"}`, session.StateWorking, nil, false},
		{"no state file", busy, "", session.StateWorking, nil, false},
		{"an unreadable state file", busy, "{", session.StateWorking, nil, false},
		{"waiting is not background", `[{"name":"r-1","sessionId":"` + id + `","status":"idle"}]`, stop, session.StateWaiting, nil, false},
		{"a failed session list is unknown", "", stop, "", errors.New("unavailable"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHome(t, "mac")
			dir := writeRun(t, h, session.Record{RunID: "r-1-a", Name: "r-1", Harness: Claude, Spawned: fixedNow()})
			if tc.file != "" {
				write(t, filepath.Join(dir, session.StateFile), tc.file, 0o600)
			}
			tm := &tmuxFake{windows: []string{"@1 %1 0 r-1"}, screens: map[string]string{"@1": "a screen"}}
			st, err := ReadStatus(Deps{Home: h.dir, Tmux: tm.tmux(), Exec: agentsFake(tc.agents, tc.err)})
			if err != nil {
				t.Fatal(err)
			}
			if w := st.Windows[0]; w.State != tc.state || w.Background != tc.background {
				t.Errorf("window %+v; want state %q, background %v", w, tc.state, tc.background)
			}
			tm.readOnly(t)
		})
	}
}
