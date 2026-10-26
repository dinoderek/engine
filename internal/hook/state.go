package hook

import (
	"encoding/json"
	"fmt"
	"io"
)

// Signal is what a session's state file records from one hook event.
type Signal struct {
	State     string
	Event     string
	SessionID string
}

// stateEvent is the part of a Codex hook event that the state hook reads.
type stateEvent struct {
	Event     string `json:"hook_event_name"`
	SessionID string `json:"session_id"`
}

// State turns Codex's session events into the session's state. Its state
// names are the ones the state file takes.
type State struct {
	Err io.Writer
	// Unattended is true when nobody watches the session: auto-review decides
	// its permission requests, so one never leaves the session waiting.
	Unattended bool
	// Write records a signal in the session's state file.
	Write func(Signal) error
}

// Handle reads a Codex hook event from in and records the session's state: a
// session start, stop, interrupt or attended permission request leaves it
// waiting, a submitted prompt or a finished tool call makes it working (the
// call after a permission request is what puts a session back to work), and a
// session end makes it ended. Any other event changes nothing. An event it cannot read or record
// gives exit 1, which blocks nothing: a hook never fails a Codex turn.
func (s State) Handle(in io.Reader) int {
	var e stateEvent
	if err := json.NewDecoder(in).Decode(&e); err != nil {
		fmt.Fprintln(s.Err, "engine hook: unreadable state event:", err)
		return 1
	}
	state := stateFor(e.Event, s.Unattended)
	if state == "" {
		return 0
	}
	if err := s.Write(Signal{State: state, Event: e.Event, SessionID: e.SessionID}); err != nil {
		fmt.Fprintln(s.Err, "engine hook: record the session state:", err)
		return 1
	}
	return 0
}

// stateFor is the state a Codex event puts the session in, or "" when the
// event says nothing about it.
func stateFor(event string, unattended bool) string {
	switch event {
	case "UserPromptSubmit", "PostToolUse":
		return "working"
	case "SessionStart", "Stop", "Interrupt":
		return "waiting"
	case "PermissionRequest":
		if !unattended {
			return "waiting"
		}
	case "SessionEnd":
		return "ended"
	}
	return ""
}
