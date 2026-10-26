package start

import (
	"fmt"

	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/tracker"
)

// KickoffArgs are what the kickoff says about the session.
type KickoffArgs struct {
	Mode, Reviewer, RunID string
	// Name is the tmux window's, and RC the Remote Control session's: none
	// for a harness without one.
	Name, RC string
}

// Kickoff is the first message of the session: the executor's kickoff line,
// plus the values its claim comment records.
func Kickoff(task tracker.Ref, k KickoffArgs) string {
	return fmt.Sprintf("Execute %s with the engine executor, %s mode. Reviewer model: %s. run=%s tmux=%s:%s rc=%s",
		task, k.Mode, k.Reviewer, k.RunID, session.Name, k.Name, k.RC)
}

// remoteControl is the Remote Control name a session of harness has, which
// only Claude's does.
func remoteControl(harness, name string) string {
	if harness == hostops.Claude {
		return name
	}
	return "none"
}
