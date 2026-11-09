package hook

import (
	"fmt"
	"io"

	"github.com/dinoderek/engine/internal/harness"
)

// Signal is what a session's state file records from one hook event.
type Signal struct {
	State     string
	Event     string
	SessionID string
	TurnID    string
}

// State turns a session's hook events into its state: all of it for a
// harness whose state comes from its hooks alone, and for the others whether
// its last turn has ended. Its state names are the ones the state file takes.
type State struct {
	Err io.Writer
	// Unattended is true when nobody watches the session: its permission
	// requests are denied or left to the harness's review, so one never
	// leaves the session waiting.
	Unattended bool
	// Write records a signal in the session's state file.
	Write func(Signal) error
}

// Handle records the session's state from a hook event: a session start,
// stop, interrupt or attended permission request leaves it waiting, a
// submitted prompt, a tool call about to run or a finished one makes it
// working (the call after a permission request is what puts a session back
// to work), and a session end makes it ended. Any other event changes
// nothing. An event it cannot record gives exit 1, which blocks nothing: a
// hook never fails a turn.
func (s State) Handle(e harness.Event) int {
	state := stateFor(e.Kind, s.Unattended)
	if state == "" {
		return 0
	}
	if err := s.Write(Signal{State: state, Event: e.Name, SessionID: e.SessionID, TurnID: e.TurnID}); err != nil {
		fmt.Fprintln(s.Err, "engine hook: record the session state:", err)
		return 1
	}
	return 0
}

// stateFor is the state an event puts the session in, or "" when the event
// says nothing about it.
func stateFor(kind harness.EventKind, unattended bool) string {
	switch kind {
	case harness.PromptSubmitted, harness.ToolStarting, harness.ToolFinished:
		return "working"
	case harness.SessionStarted, harness.TurnStopped, harness.Interrupted:
		return "waiting"
	case harness.PermissionRequested:
		if !unattended {
			return "waiting"
		}
	case harness.SessionEnded:
		return "ended"
	}
	return ""
}
