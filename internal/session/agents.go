// This file reads what the harness and git say about the sessions running on
// this host: Claude Code's list of live sessions, and the checkout a session
// works in. Engine never scrapes a pane for either.

package session

import (
	"encoding/json"
	"fmt"
	"strings"
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

// Checkout is the git checkout a directory is in.
type Checkout struct {
	// Branch is the branch checked out; empty on a detached HEAD.
	Branch string
	// Remote is the URL of origin.
	Remote string
}

// ProbeCheckout reads the branch and the origin URL of the checkout that dir
// is in. An error means dir is not in a git checkout, or has no origin.
func ProbeCheckout(run Runner, dir string) (Checkout, error) {
	branch, err := run("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return Checkout{}, err
	}
	remote, err := run("git", "-C", dir, "remote", "get-url", "origin")
	if err != nil {
		return Checkout{}, err
	}
	branch = strings.TrimSpace(branch)
	if branch == "HEAD" {
		branch = ""
	}
	return Checkout{Branch: branch, Remote: strings.TrimSpace(remote)}, nil
}

// IsRepo reports whether the checkout's origin is the GitHub repository
// owner/name, however the URL is written (https, scp-style or ssh).
func (c Checkout) IsRepo(repo string) bool {
	url := strings.ToLower(strings.TrimSuffix(strings.TrimSuffix(c.Remote, "/"), ".git"))
	repo = strings.ToLower(repo)
	return strings.HasSuffix(url, "/"+repo) || strings.HasSuffix(url, ":"+repo)
}
