package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/dinoderek/engine/internal/harness"
)

// DenyMessage is what an unattended session's agent is told when its
// permission request is denied.
const DenyMessage = "engine: this session is unattended, so the permission request was denied. " +
	"Try a route that needs no new permission; if none works, follow the Permission hard stop in the build skill (hard-stops.md)."

// Entry is one line of a run's permissions.jsonl.
type Entry struct {
	Time     time.Time       `json:"time"`
	Event    string          `json:"event"`
	Tool     string          `json:"tool"`
	Input    json.RawMessage `json:"input,omitempty"`
	Mode     string          `json:"mode,omitempty"`
	Reason   string          `json:"reason,omitempty"`
	Decision string          `json:"decision"`
}

// Permission is what the permission hook writes to and knows.
type Permission struct {
	Out, Err io.Writer
	// Log receives one Entry per logged event; nil logs nothing.
	Log io.Writer
	// Unattended is true when nobody watches the session.
	Unattended bool
	// Policy is what the harness's unattended requests get, and Deny writes
	// its denial.
	Policy harness.PermissionPolicy
	Deny   func(w io.Writer, message string) error
	Now    time.Time
}

// Handle acts on a permission request, or on the harness's own denial. An
// unattended session's request gets the harness's policy: denied, with the
// decision to Out, or left to the harness's own review; either way it is
// logged. In an attended session it writes nothing, so the harness shows its
// prompt as usual. A denial (auto mode refusing a call) is only logged. It
// returns the process exit code.
func (p Permission) Handle(e harness.Event) int {
	entry := Entry{Time: p.Now.UTC(), Event: e.Name, Tool: e.Tool, Input: e.Input, Mode: e.Mode, Reason: e.Reason}
	switch e.Kind {
	case harness.PermissionRequested:
		if !p.Unattended {
			return 0
		}
		entry.Decision = p.Policy.Decision()
		if p.Policy != harness.Deny {
			break
		}
		if err := p.Deny(p.Out, DenyMessage); err != nil {
			fmt.Fprintln(p.Err, "engine hook:", err)
			return 1
		}
	case harness.PermissionDenied:
		entry.Decision = "denied by auto mode"
	default:
		fmt.Fprintf(p.Err, "engine hook: %q is not a permission event\n", e.Name)
		return 1
	}
	if p.Log != nil {
		if err := json.NewEncoder(p.Log).Encode(entry); err != nil {
			fmt.Fprintln(p.Err, "engine hook: log the permission request:", err)
		}
	}
	return 0
}
