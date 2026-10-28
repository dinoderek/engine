package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/session"
)

var stateAt = time.Date(2026, 10, 25, 22, 30, 0, 0, time.UTC)

// recordedEvent is a Codex hook event cut to the fields the hook reads; the
// hook package's tests run the full recordings.
func recordedEvent(t *testing.T, name string) string {
	t.Helper()
	return `{"session_id":"` + codexSession + `","turn_id":"turn-a","hook_event_name":"` + name + `","tool_name":"Bash"}`
}

const codexSession = "01a1752f-8cf9-7592-bdbd-7a42730e503a"

func runStateHook(in string, vars map[string]string) (int, string) {
	var errOut bytes.Buffer
	env := Env{In: strings.NewReader(in), Out: &bytes.Buffer{}, Err: &errOut, Getenv: func(k string) string { return vars[k] },
		Now: func() time.Time { return stateAt }}
	return Run([]string{"hook", "state"}, env), errOut.String()
}

// Every state the hook writes is one the reader accepts, so a status that
// reads the file never calls a recorded event's state unknown.
func TestStateHookWritesTheStateFile(t *testing.T) {
	tests := []struct {
		event string
		want  string
	}{
		{"SessionStart", session.StateWaiting},
		{"UserPromptSubmit", session.StateWorking},
		{"PostToolUse", session.StateWorking},
		{"Stop", session.StateWaiting},
		{"Interrupt", session.StateWaiting},
		{"PermissionRequest", session.StateWaiting},
		{"SessionEnd", session.StateEnded},
	}
	for _, tt := range tests {
		t.Run(tt.event, func(t *testing.T) {
			dir := t.TempDir()
			code, stderr := runStateHook(recordedEvent(t, tt.event), map[string]string{session.RunDirVar: dir})
			if code != 0 {
				t.Fatalf("exit %d, stderr %q", code, stderr)
			}
			got, ok := session.ReadHarnessState(dir)
			want := session.HarnessState{State: tt.want, Event: tt.event, At: stateAt, SessionID: codexSession, TurnID: "turn-a"}
			if !ok || got != want {
				t.Errorf("state %+v (read %v), want %+v", got, ok, want)
			}
		})
	}
}

func TestStateHookDoesNothingElsewhere(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	tests := map[string]struct {
		in   string
		vars map[string]string
		code int
		msg  string
	}{
		"outside an engine run":        {recordedEvent(t, "Stop"), nil, 0, ""},
		"an unattended denial":         {recordedEvent(t, "PermissionRequest"), map[string]string{session.RunDirVar: dir, session.UnattendedVar: "1"}, 0, ""},
		"an event that says nothing":   {recordedEvent(t, "PreToolUse"), map[string]string{session.RunDirVar: dir}, 0, ""},
		"an unreadable event":          {"not json", map[string]string{session.RunDirVar: dir}, 1, "unreadable state event"},
		"a run directory that is gone": {recordedEvent(t, "Stop"), map[string]string{session.RunDirVar: filepath.Join(dir, "gone")}, 1, "record the session state"},
	}
	t.Chdir(cwd) // a hook that wrote to "" would leave its file here
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			code, stderr := runStateHook(tt.in, tt.vars)
			if code != tt.code || !strings.Contains(stderr, tt.msg) {
				t.Errorf("exit %d, stderr %q; want %d, %q", code, stderr, tt.code, tt.msg)
			}
			for _, where := range []string{dir, cwd} {
				if _, ok := session.ReadHarnessState(where); ok {
					t.Errorf("a state file was written in %s", where)
				}
			}
		})
	}
}
