package claude

import (
	"encoding/json"
	"io"

	"github.com/dinoderek/engine/internal/harness"
)

// statuslineCommand is the status line the launch's --settings sets.
const statuslineCommand = "engine hook statusline --harness " + Name

// kinds are the Claude Code hook events engine acts on.
var kinds = map[string]harness.EventKind{
	"SessionStart":      harness.SessionStarted,
	"UserPromptSubmit":  harness.PromptSubmitted,
	"PreToolUse":        harness.ToolStarting,
	"PostToolUse":       harness.ToolFinished,
	"PermissionRequest": harness.PermissionRequested,
	"PermissionDenied":  harness.PermissionDenied,
	"Stop":              harness.TurnStopped,
	"SessionEnd":        harness.SessionEnded,
}

// shellTool is the tool Claude Code runs shell commands with.
const shellTool = "Bash"

// Hooks reads Claude Code's hook events. An unattended request is denied:
// Claude Code would otherwise prompt in a window nobody watches. Every
// session has the launch's variables, so its hooks never look for their run.
func (a Adapter) Hooks() harness.Hooks { return hooks(a) }

type hooks Adapter

// event is the part of a Claude Code hook event engine reads.
type event struct {
	Name      string          `json:"hook_event_name"`
	SessionID string          `json:"session_id"`
	TurnID    string          `json:"turn_id"`
	Cwd       string          `json:"cwd"`
	Tool      string          `json:"tool_name"`
	Input     json.RawMessage `json:"tool_input"`
	Mode      string          `json:"permission_mode"`
	Reason    string          `json:"reason"`
}

func (hooks) Event(raw []byte) (harness.Event, error) {
	var e event
	if err := json.Unmarshal(raw, &e); err != nil {
		return harness.Event{}, err
	}
	ev := harness.Event{Kind: harness.Other, Name: e.Name, SessionID: e.SessionID, TurnID: e.TurnID, Cwd: e.Cwd,
		Tool: e.Tool, Input: e.Input, Mode: e.Mode, Reason: e.Reason}
	if k, ok := kinds[e.Name]; ok {
		ev.Kind = k
	}
	if e.Tool == shellTool {
		var in struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal(e.Input, &in)
		ev.Command = in.Command
	}
	return ev, nil
}

// Deny writes Claude Code's PermissionRequest decision.
func (hooks) Deny(w io.Writer, message string) error {
	type decision struct {
		Behavior string `json:"behavior"`
		Message  string `json:"message"`
	}
	type specific struct {
		Event    string   `json:"hookEventName"`
		Decision decision `json:"decision"`
	}
	return json.NewEncoder(w).Encode(struct {
		Output specific `json:"hookSpecificOutput"`
	}{specific{"PermissionRequest", decision{"deny", message}}})
}

func (hooks) Unattended() harness.PermissionPolicy { return harness.Deny }

func (hooks) FindsRun() bool { return false }

// Signalled does nothing: Claude Code needs nothing after a state.
func (hooks) Signalled(harness.HookContext, harness.Event) {}

// Entries are the status line, which records the session's quota, and the
// limit hook, which keeps its usage-limit marker.
func (h hooks) Entries() map[string]harness.Entry {
	return map[string]harness.Entry{
		"statusline": func(c harness.Call) error {
			return statusline(c.In, c.Out, c.RunDir, h.env.Time())
		},
		"limit": func(c harness.Call) error {
			return recordLimit(c.In, c.RunDir, h.env.Time())
		},
	}
}
