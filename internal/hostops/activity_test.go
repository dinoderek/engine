package hostops

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/session"
)

// touch writes a file at path with modification time mod.
func touch(t *testing.T, path string, mod time.Time) {
	t.Helper()
	write(t, path, "x\n", 0o644)
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func TestLastActivityIsTheNewerOfTheTranscriptAndThePaneLog(t *testing.T) {
	now := fixedNow()
	for _, tc := range []struct {
		name       string
		transcript *time.Time // nil: no transcript file; the state names it either way
		pane       *time.Time
		state      bool
		want       time.Time
	}{
		{"a newer transcript", ptr(now.Add(-time.Minute)), ptr(now.Add(-time.Hour)), true, now.Add(-time.Minute)},
		{"a newer pane log", ptr(now.Add(-2 * time.Hour)), ptr(now.Add(-time.Minute)), true, now.Add(-time.Minute)},
		{"a missing transcript uses the pane log", nil, ptr(now.Add(-time.Hour)), true, now.Add(-time.Hour)},
		{"a state naming no transcript uses the pane log", nil, ptr(now.Add(-3 * time.Hour)), false, now.Add(-3 * time.Hour)},
		{"a missing pane log uses the transcript", ptr(now.Add(-time.Hour)), nil, true, now.Add(-time.Hour)},
		{"neither is unknown", nil, nil, true, time.Time{}},
	} {
		dir := t.TempDir()
		transcript := filepath.Join(dir, "transcript.jsonl")
		if tc.transcript != nil {
			touch(t, transcript, *tc.transcript)
		}
		if tc.pane != nil {
			touch(t, filepath.Join(dir, session.PaneLog), *tc.pane)
		}
		st := session.HarnessState{State: session.StateWorking, Event: "PreToolUse", At: now}
		if tc.state {
			st.Transcript = transcript
		}
		if err := session.WriteHarnessState(dir, st); err != nil {
			t.Fatal(err)
		}
		want := int64(0)
		if !tc.want.IsZero() {
			want = tc.want.Unix()
		}
		if got := lastActivity(session.Run{Dir: dir}); got != want {
			t.Errorf("%s: got %d, want %d", tc.name, got, want)
		}
	}
}

func ptr[T any](v T) *T { return &v }

// A window's last activity is on the wire, and a working window that has
// been quiet for QuietWait shows its screen as a waiting one does; a recent
// one, a dead one and one of unknown activity show none.
func TestStatusReportsActivityAndTheScreenOfAQuietWorkingWindow(t *testing.T) {
	h := newHome(t, "mac")
	now := fixedNow()
	tm := &tmuxFake{
		windows: []string{"@1 %1 0 r-1", "@2 %2 0 r-2", "@3 %3 0 r-3", "@4 %4 1 r-4"},
		screens: map[string]string{"@1": "quiet screen", "@2": "recent screen", "@3": "unknown screen", "@4": "dead screen"},
	}
	at := fixedNow()
	quiet := writeRun(t, h, session.Record{RunID: "r-1-a", Name: "r-1", Harness: Claude, Spawned: at})
	recent := writeRun(t, h, session.Record{RunID: "r-2-a", Name: "r-2", Harness: Claude, Spawned: at})
	writeRun(t, h, session.Record{RunID: "r-3-a", Name: "r-3", Harness: Claude, Spawned: at})
	dead := writeRun(t, h, session.Record{RunID: "r-4-a", Name: "r-4", Harness: Claude, Spawned: at})
	touch(t, filepath.Join(quiet, session.PaneLog), now.Add(-QuietWait))
	touch(t, filepath.Join(recent, session.PaneLog), now.Add(-QuietWait+time.Second))
	touch(t, filepath.Join(dead, session.PaneLog), now.Add(-2*QuietWait))
	d := Deps{Tmux: tm.tmux(), Home: h.dir, Now: fixedNow,
		Exec: agentsFake(`[{"pid":1,"name":"r-1","status":"busy"},{"pid":2,"name":"r-2","status":"busy"},{"pid":3,"name":"r-3","status":"busy"}]`, nil)}
	got, err := ReadStatus(d)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Window{}
	for _, w := range got.Windows {
		byName[w.Name] = w
	}
	if w := byName["r-1"]; w.LastActivity != now.Add(-QuietWait).Unix() || w.Screen != "quiet screen" {
		t.Errorf("quiet window: %+v", w)
	}
	if w := byName["r-2"]; w.LastActivity != now.Add(-QuietWait+time.Second).Unix() || w.Screen != "" {
		t.Errorf("recent window: %+v", w)
	}
	if w := byName["r-3"]; w.LastActivity != 0 || w.Screen != "" {
		t.Errorf("window of unknown activity: %+v", w)
	}
	if w := byName["r-4"]; w.LastActivity != 0 {
		t.Errorf("dead window: %+v", w)
	}
	tm.readOnly(t)
}
