package ui

import "github.com/dinoderek/engine/internal/control"

// Action names, one per control the page can offer. Each is a write route
// (writeRoutes); the page knows its wording and whether it confirms.
const (
	actApprove          = "approve"
	actRun              = "run"
	actStop             = "stop"
	actTaskStop         = "task-stop"
	actResume           = "resume"
	actRetry            = "retry"
	actCompleteExternal = "complete-external"
)

// Controls is what the page needs to offer an item's operator actions: the
// version it read, which the versioned writes send back, and the actions the
// item's state allows. The control server still decides every write.
type Controls struct {
	Version int      `json:"version"`
	Actions []string `json:"actions"`
}

// planActions are the writes a plan's stored state allows (control.Item.State,
// not the derived status): a prepared plan is approved, an approved one that
// is not coordinating is run, a coordinating one is stopped, a done one has
// none.
func planActions(state string, approved, coordinating bool) []string {
	switch {
	case state == "prepare":
		return []string{actApprove}
	case state == "done" || !approved:
		return []string{}
	case coordinating:
		return []string{actStop}
	default:
		return []string{actRun}
	}
}

// taskActions are the writes a task's state allows. active says an attempt of
// the task has not ended. A running, in-review or needs-human task with an
// execution can be stopped; a held task, which is todo, can be resumed; a
// failed one retried; a todo or failed one with no execution can be completed
// by hand.
func taskActions(state string, held, active bool) []string {
	out := []string{}
	switch state {
	case "running", "in-review", "needs-human":
		if active {
			out = append(out, actTaskStop)
		}
	case "todo":
		if held {
			out = append(out, actResume)
		}
	case "failed":
		out = append(out, actRetry)
	}
	if (state == "todo" || state == "failed") && !active {
		out = append(out, actCompleteExternal)
	}
	return out
}

// itemControls is the Controls of a plan or task record. active is a task's
// live execution; a plan ignores it.
func itemControls(it control.Item, active bool) Controls {
	if it.Kind == "plan" {
		return Controls{Version: it.Version, Actions: planActions(it.State, it.ApprovedVersion > 0, it.Coordinating)}
	}
	return Controls{Version: it.Version, Actions: taskActions(it.State, it.Held, active)}
}

// liveAttempt reports whether the attempt id is one that has not ended.
func liveAttempt(attempts []control.Attempt, id string) bool {
	for _, at := range attempts {
		if at.ID == id && at.State != "ended" {
			return true
		}
	}
	return false
}
