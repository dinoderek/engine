// This file is what a session is doing: working, or waiting for input. Each
// harness says it its own way. Claude Code lists its live sessions; a harness
// that cannot be asked writes a state file to the run directory from its
// hooks. A state engine cannot read is unknown, never dead.

package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// The states of a session. A window's state is working, waiting, limited or unknown
// (""); ended appears only in a state file, and a live window whose file says
// ended is unknown.
const (
	StateWorking = "working"
	StateWaiting = "waiting"
	StateEnded   = "ended"
)

// StateFile is the harness's state file in a run directory.
const StateFile = "harness-state.json"

// HarnessState is the state file: the state, the harness event that set it,
// when, and the harness's own session ID once it is known.
type HarnessState struct {
	State     string    `json:"state"`
	Event     string    `json:"event"`
	At        time.Time `json:"at"`
	SessionID string    `json:"session_id,omitempty"`
	TurnID    string    `json:"turn_id,omitempty"`
	ResetsAt  int64     `json:"resets_at,omitempty"` // limit reset, epoch seconds
}

// WriteHarnessState writes the state file in dir atomically: a reader sees
// the old file or the new one, never half of either.
func WriteHarnessState(dir string, s HarnessState) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = replaceFile(filepath.Join(dir, StateFile), data, 0o644, func() bool { return true })
	return err
}

// ReadHarnessState reads the state file in dir. A file that is missing or
// unreadable, or that names a state engine does not know, gives false: the
// session's state is unknown.
func ReadHarnessState(dir string) (HarnessState, bool) {
	var s HarnessState
	data, err := os.ReadFile(filepath.Join(dir, StateFile))
	if err != nil || json.Unmarshal(data, &s) != nil {
		return HarnessState{}, false
	}
	switch s.State {
	case StateWorking, StateWaiting, StateLimited, StateEnded:
		return s, true
	}
	return HarnessState{}, false
}

// AgentState is the state of a live Claude Code session: busy is working;
// waiting and idle are waiting, because only a prompt moves the session on.
// A status a newer Claude Code adds is unknown.
func AgentState(a Agent) string {
	switch a.Status {
	case AgentBusy:
		return StateWorking
	case AgentWaiting, AgentIdle:
		return StateWaiting
	}
	return ""
}
