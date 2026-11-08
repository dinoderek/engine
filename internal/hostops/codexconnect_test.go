package hostops

import (
	"errors"
	"strings"
	"testing"
)

// A daemon with remote control on but no live connection to the apps still
// takes the session, and the reply says why the apps do not show it.
func TestSpawnCodexJoinsADisconnectedDaemonWithAWarning(t *testing.T) {
	tests := []struct {
		name, etime, log string
		logErr           error
		want             string
	}{
		{"never connected", "00:30", "", nil, "no connection logged since the daemon started"},
		{"connection lost", "00:30", `[{"body":"remote control websocket reader disconnected, err: websocket disconnected"}]`, nil, "websocket reader disconnected"},
		{"reconnecting", "00:30", `[{"body":"remote control websocket status changed previous_status=Connected next_status=Connecting"}]`, nil, "next_status=Connecting"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHome(t, "mac")
			h.clone(t)
			h.engineCodex(t)
			h.fakeCodex(t)
			tm := &tmuxFake{}
			d := connected(h.dir)
			d.etime, d.log, d.logErr = tt.etime, tt.log, tt.logErr
			got, err := Spawn(codexSpawnDeps(t, h, tm, d, nil), codexRequest(HandsOff))
			if err != nil || !strings.Contains(tm.command, "--remote unix://") {
				t.Fatalf("not joined: %+v, %v, %s", got, err, tm.command)
			}
			if !strings.Contains(got.Warning, "remote control is not connected") || !strings.Contains(got.Warning, tt.want) {
				t.Errorf("warning %q, want %q", got.Warning, tt.want)
			}
		})
	}
}

// A connection that cannot be read is unknown, not a disconnection: the
// session joins and the warning says so.
func TestSpawnCodexJoinsWhenTheConnectionCannotBeRead(t *testing.T) {
	tests := []struct {
		name, etime, log string
		logErr           error
		want             string
	}{
		{"no sqlite3", "00:30", "", errors.New("exec: \"sqlite3\": executable file not found"), "its log cannot be read: exec: \"sqlite3\""},
		{"reply unreadable", "00:30", "Error: oops", nil, "its log reply cannot be read"},
		{"start unreadable", "soon", connectedLog, nil, "its start cannot be read"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHome(t, "mac")
			h.clone(t)
			h.engineCodex(t)
			h.fakeCodex(t)
			tm := &tmuxFake{}
			d := connected(h.dir)
			d.etime, d.log, d.logErr = tt.etime, tt.log, tt.logErr
			got, err := Spawn(codexSpawnDeps(t, h, tm, d, nil), codexRequest(HandsOff))
			if err != nil || !strings.Contains(tm.command, "--remote unix://") {
				t.Fatalf("not joined: %+v, %v, %s", got, err, tm.command)
			}
			if !strings.Contains(got.Warning, "whether the ChatGPT and Codex apps show it is unknown") || !strings.Contains(got.Warning, tt.want) {
				t.Errorf("warning %q, want %q", got.Warning, tt.want)
			}
		})
	}
}
