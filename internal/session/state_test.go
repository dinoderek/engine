package session

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestHarnessStateRoundTrips(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 10, 25, 22, 0, 0, 0, time.UTC)
	for _, want := range []HarnessState{
		{State: StateWaiting, Event: "SessionStart", At: at, SessionID: "019a-codex"},
		{State: StateWorking, Event: "UserPromptSubmit", At: at.Add(time.Minute)},
		{State: StateLimited, Event: "Stop", At: at, ResetsAt: at.Add(time.Hour).Unix()},
		{State: StateEnded, Event: "SessionEnd", At: at.Add(time.Hour), SessionID: "019a-codex"},
	} {
		if err := WriteHarnessState(dir, want); err != nil {
			t.Fatal(err)
		}
		got, ok := ReadHarnessState(dir)
		if !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("got %+v, %v; want %+v", got, ok, want)
		}
	}
	// The write renames a whole file into place, and leaves nothing beside it.
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != StateFile {
		t.Errorf("run directory holds %v, %v", entries, err)
	}
}

func TestAnUnreadableStateFileIsUnknown(t *testing.T) {
	cases := map[string]string{
		"corrupt":       `{"state":"work`,
		"not an object": `"working"`,
		"unheard-of":    `{"state":"thinking","event":"Stop","at":"2026-10-25T22:00:00Z"}`,
		"no state":      `{"event":"Stop","at":"2026-10-25T22:00:00Z"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, StateFile), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			if got, ok := ReadHarnessState(dir); ok || got != (HarnessState{}) {
				t.Errorf("got %+v, %v", got, ok)
			}
		})
	}
	if got, ok := ReadHarnessState(t.TempDir()); ok {
		t.Errorf("a missing file read as %+v", got)
	}
	if got, ok := ReadHarnessState(filepath.Join(t.TempDir(), "no-such-run")); ok {
		t.Errorf("a missing run directory read as %+v", got)
	}
}

func TestWriteHarnessStateFailsWithoutItsRunDirectory(t *testing.T) {
	if err := WriteHarnessState(filepath.Join(t.TempDir(), "gone"), HarnessState{State: StateWorking}); err == nil {
		t.Error("wrote a state file into a missing run directory")
	}
}

func TestAgentState(t *testing.T) {
	for status, want := range map[string]string{
		AgentBusy: StateWorking, AgentWaiting: StateWaiting, AgentIdle: StateWaiting, "unheard-of": "", "": "",
	} {
		if got := AgentState(Agent{Status: status}); got != want {
			t.Errorf("%q: got %q, want %q", status, got, want)
		}
	}
}
