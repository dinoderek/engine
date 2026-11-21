package harness

import (
	"encoding/json"
	"io"
)

// CoreEntries are the `engine hook <entry>` commands every harness gets, which
// the core runs on the adapter's events: the merge guard, the permission log,
// the state file and the prompt hook, which records the operator's pane input
// on the server. An adapter's own entries never take these names.
var CoreEntries = []string{"pre-tool-use", "permission", "state", "prompt"}

// Hooks translates a harness's hook events into engine's, and engine's
// replies back. The core owns the merge guard, the permission log and the
// state file; the adapter owns the event shapes and its own entries.
type Hooks interface {
	// Event reads one hook event in the harness's own JSON.
	Event(raw []byte) (Event, error)
	// Deny writes a permission request's denial, telling the agent message,
	// in the harness's shape.
	Deny(w io.Writer, message string) error
	// Unattended is what an unattended session's permission request gets.
	Unattended() PermissionPolicy
	// FindsRun is true when a hook that has no run directory in its
	// environment finds its run by the event's cwd, among this harness's
	// runs: a session that joined a daemon loses the launch's environment.
	FindsRun() bool
	// Signalled runs after the state hook recorded ev for the run in c. It
	// never fails the hook: what goes wrong it reports to c.Err.
	Signalled(c HookContext, ev Event)
	// Entries are the adapter's own `engine hook <entry>` commands, by entry.
	Entries() map[string]Entry
	// Prompts are the prompts the session whose transcript is at path
	// submitted, as the harness recorded them there, each saying whether
	// the harness injected it; a transcript it cannot read is an error.
	Prompts(transcript string) ([]Prompt, error)
}

// Prompt is one prompt in a session's transcript.
type Prompt struct {
	Text string
	// Injected is true when the transcript marks the turn as one the
	// harness added, such as a subagent's report or a background task's
	// notice, rather than one a person typed.
	Injected bool
}

// EventKind is what a hook event means to engine.
type EventKind string

// The kinds of hook event engine acts on; Other is every other event.
const (
	Other               EventKind = "other"
	SessionStarted      EventKind = "session-started"
	PromptSubmitted     EventKind = "prompt-submitted"
	ToolStarting        EventKind = "tool-starting"
	ToolFinished        EventKind = "tool-finished"
	PermissionRequested EventKind = "permission-requested"
	PermissionDenied    EventKind = "permission-denied"
	TurnStopped         EventKind = "turn-stopped"
	Interrupted         EventKind = "interrupted"
	SessionEnded        EventKind = "session-ended"
)

// Event is a hook event in engine's terms.
type Event struct {
	Kind EventKind
	// Name is the harness's own name for the event, which the state file and
	// the permission log record as it is.
	Name                   string
	SessionID, TurnID, Cwd string
	// Tool is the tool a tool or permission event is about, as the harness
	// names it; Input is its input, as the harness sent it.
	Tool  string
	Input json.RawMessage
	// Command is the shell command, when Tool is the harness's shell.
	Command string
	// Mode is the session's permission mode; Reason why the harness denied a
	// call, on a PermissionDenied event.
	Mode, Reason string
	// Prompt is the text a PromptSubmitted event submitted, as the harness
	// sent it; Transcript is the path of the session's transcript.
	Prompt, Transcript string
}

// PermissionPolicy is what an unattended session's permission request gets.
type PermissionPolicy int

const (
	// Deny refuses the request, so it never waits for a person.
	Deny PermissionPolicy = iota
	// LeaveToReview leaves the request to the harness's own review, which
	// decides without a person after the hook.
	LeaveToReview
)

// Decision is how the permission log records the policy's answer.
func (p PermissionPolicy) Decision() string {
	if p == LeaveToReview {
		return "left to auto-review"
	}
	return "deny"
}

// HookRun is the engine run a hook acts for.
type HookRun struct {
	// Dir is the run directory, or "" outside an engine run.
	Dir string
	// Repo is the run's owner/name, from its record, when the hook found the
	// run by its cwd; a check run has none.
	Repo string
	// Joined is true when the hook found the run by its cwd (FindsRun).
	Joined bool
	// Unattended is true when nobody watches the session.
	Unattended bool
}

// HookContext is what Signalled may use.
type HookContext struct {
	Run HookRun
	Err io.Writer
	// Detach starts `engine hook <entry> --harness <this harness> <args>` in
	// the background, without waiting; nil when it cannot.
	Detach func(entry string, args ...string) error
}

// Call is one run of an adapter's own entry.
type Call struct {
	In       io.Reader
	Out, Err io.Writer
	// RunDir is the run directory the launch exported, or "".
	RunDir string
	// Args follow `--harness <name>` on the command line.
	Args []string
}

// Entry runs an adapter's own hook. An error exits 1, which blocks nothing.
type Entry func(Call) error
