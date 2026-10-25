package run

import (
	"testing"

	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/session"
)

// The session check reads only the state status gives the session's window,
// whatever its harness: Claude Code's raw list is not consulted.
func TestSeeReadsTheWindowsState(t *testing.T) {
	codex := func(state string, alive bool) hostops.Window {
		return hostops.Window{Name: "r-2", ID: "@4", Alive: alive, Harness: hostops.Codex, State: state}
	}
	agents := []session.Agent{{Name: "r-2", Status: session.AgentWaiting}}
	cases := []struct {
		name                 string
		win                  hostops.Window
		live, waiting, known bool
	}{
		{"working", codex(session.StateWorking, true), true, false, true},
		{"waiting", codex(session.StateWaiting, true), true, true, true},
		{"unknown", codex("", true), true, false, false},
		{"dead, whatever it said", codex(session.StateWaiting, false), false, false, true},
		{"another window of the name", hostops.Window{Name: "r-2", ID: "@9", Alive: true, State: session.StateWaiting}, false, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := Session{Name: "r-2", WindowID: "@4"}
			h := HostLoad{Name: "devbox", Status: hostops.Status{Windows: []hostops.Window{tc.win}, Agents: agents}}
			known := see(&s, h, "r-2")
			if s.Live != tc.live || s.Waiting != tc.waiting || known != tc.known {
				t.Errorf("live %v, waiting %v, known %v; want %v, %v, %v", s.Live, s.Waiting, known, tc.live, tc.waiting, tc.known)
			}
		})
	}
}
