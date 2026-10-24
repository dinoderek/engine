package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// DenyMessage is what an unattended session's agent is told when its
// permission request is denied.
const DenyMessage = "engine: this session is unattended, so the permission request was denied. " +
	"Try a route that needs no new permission; if none works, follow the Permission hard stop in the engine skill (rulings.md)."

// permissionEvent is the part of a Claude Code PermissionRequest or
// PermissionDenied event that engine reads and logs.
type permissionEvent struct {
	Event     string          `json:"hook_event_name"`
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
	Mode      string          `json:"permission_mode"`
	Reason    string          `json:"reason,omitempty"`
}

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
	Now        time.Time
}

// Handle reads a Claude Code PermissionRequest or PermissionDenied event
// from in. An unattended session's request is denied: the decision goes to
// Out and the request to Log. In an attended session it writes nothing, so
// Claude Code shows its prompt as usual. A PermissionDenied event (auto mode
// refusing a call) is only logged. It returns the process exit code.
func (p Permission) Handle(in io.Reader) int {
	var e permissionEvent
	if err := json.NewDecoder(in).Decode(&e); err != nil {
		fmt.Fprintln(p.Err, "engine hook: unreadable permission event:", err)
		return 1
	}
	entry := Entry{Time: p.Now.UTC(), Event: e.Event, Tool: e.ToolName, Input: e.ToolInput, Mode: e.Mode, Reason: e.Reason}
	switch e.Event {
	case "PermissionRequest":
		if !p.Unattended {
			return 0
		}
		entry.Decision = "deny"
		if err := writeDeny(p.Out); err != nil {
			fmt.Fprintln(p.Err, "engine hook:", err)
			return 1
		}
	case "PermissionDenied":
		entry.Decision = "denied by auto mode"
	default:
		fmt.Fprintf(p.Err, "engine hook: %q is not a permission event\n", e.Event)
		return 1
	}
	if p.Log != nil {
		if err := json.NewEncoder(p.Log).Encode(entry); err != nil {
			fmt.Fprintln(p.Err, "engine hook: log the permission request:", err)
		}
	}
	return 0
}

func writeDeny(out io.Writer) error {
	type decision struct {
		Behavior string `json:"behavior"`
		Message  string `json:"message"`
	}
	type specific struct {
		Event    string   `json:"hookEventName"`
		Decision decision `json:"decision"`
	}
	return json.NewEncoder(out).Encode(struct {
		Output specific `json:"hookSpecificOutput"`
	}{specific{"PermissionRequest", decision{"deny", DenyMessage}}})
}
