// This file reads what the harness and git say about the sessions running on
// this host: Claude Code's list of live sessions, and the checkout a session
// works in. Engine never scrapes a pane for either.

package session

import (
	"encoding/json"
	"fmt"
)

// Agent statuses that Claude Code reports for a live session.
const (
	AgentBusy    = "busy"
	AgentWaiting = "waiting"
	AgentIdle    = "idle"
)

// Agent is a live Claude Code session, as `claude agents --json` lists it.
// The fields engine does not use are left out.
type Agent struct {
	PID  int    `json:"pid"`
	CWD  string `json:"cwd"`
	Name string `json:"name"`
	// SessionID is Claude Code's own ID for the session, the one its hook
	// events carry.
	SessionID string `json:"sessionId"`
	// Status is busy, waiting or idle; a newer Claude Code may report another.
	Status string `json:"status"`
	// WaitingFor says what a waiting session needs, when Claude Code says.
	WaitingFor string `json:"waitingFor"`
}

// ListAgents runs `<bin> agents --json` and returns the live sessions on this
// host, whether or not they run in tmux.
func ListAgents(run Runner, bin string) ([]Agent, error) {
	out, err := run(bin, "agents", "--json")
	if err != nil {
		return nil, fmt.Errorf("%s agents: %w", bin, err)
	}
	var agents []Agent
	if err := json.Unmarshal([]byte(out), &agents); err != nil {
		return nil, fmt.Errorf("%s agents --json: unreadable output: %w", bin, err)
	}
	return agents, nil
}
