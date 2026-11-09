package codex

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/session"
)

// kinds are the Codex hook events engine acts on.
var kinds = map[string]harness.EventKind{
	"SessionStart":      harness.SessionStarted,
	"UserPromptSubmit":  harness.PromptSubmitted,
	"PreToolUse":        harness.ToolStarting,
	"PostToolUse":       harness.ToolFinished,
	"PermissionRequest": harness.PermissionRequested,
	"Stop":              harness.TurnStopped,
	"Interrupt":         harness.Interrupted,
	"SessionEnd":        harness.SessionEnded,
}

// shellTool is the name Codex gives its shell tool in hook events, the
// same as Claude Code's, so the merge guard reads both alike.
const shellTool = "Bash"

// labelKinds start an attempt to label a joined session's thread: the first
// prompt, then each tool call and stop until one succeeds or the attempts
// run out, since the first may come before the daemon has stored the thread.
var labelKinds = map[harness.EventKind]bool{harness.PromptSubmitted: true, harness.ToolFinished: true, harness.TurnStopped: true}

// Hooks reads Codex's hook events. Codex asks the permission hook before its
// auto-review, which decides without a person, so an unattended request is
// left to it.
func (a Adapter) Hooks() harness.Hooks { return hooks(a) }

type hooks Adapter

// event is the part of a Codex hook event engine reads.
type event struct {
	Name      string          `json:"hook_event_name"`
	SessionID string          `json:"session_id"`
	TurnID    string          `json:"turn_id"`
	Cwd       string          `json:"cwd"`
	Tool      string          `json:"tool_name"`
	Input     json.RawMessage `json:"tool_input"`
	Mode      string          `json:"permission_mode"`
}

func (hooks) Event(raw []byte) (harness.Event, error) {
	var e event
	if err := json.Unmarshal(raw, &e); err != nil {
		return harness.Event{}, err
	}
	ev := harness.Event{Kind: harness.Other, Name: e.Name, SessionID: e.SessionID, TurnID: e.TurnID, Cwd: e.Cwd,
		Tool: e.Tool, Input: e.Input, Mode: e.Mode}
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

// Deny writes Codex's PermissionRequest decision.
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

func (hooks) Unattended() harness.PermissionPolicy { return harness.LeaveToReview }

func (hooks) FindsRun() bool { return true }

// Signalled starts the label entry in the background for a joined task
// session whose thread is not yet labelled. A preflight check's run names no
// repo, so it has no title or project to set. The hook runs inside the
// daemon the label calls, so it never waits for it. A failure to start is
// reported, and the session carries on.
func (h hooks) Signalled(c harness.HookContext, ev harness.Event) {
	if !c.Run.Joined || c.Run.Repo == "" || !labelKinds[ev.Kind] || ev.SessionID == "" || !LabelPending(c.Run.Dir, h.env.Time()) {
		return
	}
	if c.Detach == nil {
		fmt.Fprintln(c.Err, "engine hook: label the Codex thread: no way to start it in the background")
		return
	}
	if err := c.Detach(labelEntry, c.Run.Dir, ev.SessionID); err != nil {
		fmt.Fprintln(c.Err, "engine hook: label the Codex thread:", err)
	}
}

// labelEntry is `engine hook label --harness codex <run-dir> <thread>`.
const labelEntry = "label"

// Entries is the label entry, which labels the thread of the run in
// <run-dir> and records the outcome there.
func (h hooks) Entries() map[string]harness.Entry {
	return map[string]harness.Entry{labelEntry: func(c harness.Call) error {
		if len(c.Args) != 2 {
			return fmt.Errorf("want <run-dir> <thread>, got %d arguments", len(c.Args))
		}
		rec, err := session.ReadRecord(c.Args[0])
		if err != nil {
			return err
		}
		return Adapter(h).LabelThread(c.Args[0], rec, c.Args[1])
	}}
}
