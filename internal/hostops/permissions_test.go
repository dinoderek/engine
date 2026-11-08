package hostops

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/session"
)

const (
	allowLine  = `{"time":"2026-10-30T19:10:00Z","event":"PermissionRequest","tool":"Read","input":{"file_path":"/a"},"decision":"left to auto-review"}` + "\n"
	deniedLine = `{"time":"2026-10-30T19:13:06Z","event":"PermissionDenied","tool":"Bash","input":{"command":"gh pr merge 77"},"mode":"auto","reason":"[Merge Without Review]","decision":"denied by auto mode"}` + "\n"
)

var denied = &Denial{At: time.Date(2026, 10, 30, 19, 13, 6, 0, time.UTC), Tool: "Bash", Input: []byte(`{"command":"gh pr merge 77"}`),
	Decision: "denied by auto mode", Reason: "[Merge Without Review]"}

func TestLastDenial(t *testing.T) {
	for _, tc := range []struct {
		name, log string
		want      *Denial
	}{
		{"no log", "", nil},
		{"a denial last", allowLine + deniedLine, denied},
		{"a request left to auto-review last", deniedLine + allowLine, nil},
		{"an unreadable log", deniedLine + "{not json\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), session.PermissionLog)
			if tc.log != "" {
				write(t, path, tc.log, 0o600)
			}
			if got := LastDenial(path); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// Status sends a waiting session's newest denial, and none for a session
// that works.
func TestStatusSendsAWaitingSessionsDenial(t *testing.T) {
	h := newHome(t, "mac")
	at := fixedNow()
	for _, name := range []string{"r-1", "r-2"} {
		dir := writeRun(t, h, session.Record{RunID: name + "-a", Name: name, Harness: Claude, Spawned: at})
		write(t, filepath.Join(dir, session.PermissionLog), deniedLine, 0o600)
	}
	tm := &tmuxFake{windows: []string{"@1 %1 0 r-1", "@2 %2 0 r-2"}}
	agents := `[{"name":"r-1","status":"busy"},{"name":"r-2","status":"idle"}]`
	got, err := ReadStatus(Deps{Tmux: tm.tmux(), Home: h.dir, Exec: agentsFake(agents, nil)})
	if err != nil {
		t.Fatal(err)
	}
	if got.Windows[0].Denial != nil || !reflect.DeepEqual(got.Windows[1].Denial, denied) {
		t.Errorf("denials %+v, %+v", got.Windows[0].Denial, got.Windows[1].Denial)
	}
}

// A denial survives the JSON a remote host's status reply travels as.
func TestADenialCrossesTheStatusReply(t *testing.T) {
	in := Status{Windows: []Window{{Name: "r-1", ID: "@1", Alive: true, State: session.StateWaiting, Denial: denied}}}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Status
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out.Windows[0].Denial, denied) {
		t.Errorf("got %+v", out.Windows[0].Denial)
	}
}
