package controlrun

import (
	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/reconcile"
)

// plannerEnded tells the operator how a dead planner's attempt at ended: a
// planner that wrote tasks leaves the plan to go on with them; one that
// wrote none is started again once, and a second such death stops
// coordination, with both deaths' pane log tails.
func (c *Coordinator) plannerEnded(st control.State, a reconcile.Action, at control.Attempt) error {
	switch at.Reason {
	case control.EndVanished:
		return c.notice(a.Item, "vanished/"+at.ID, a.Text+" after writing tasks; the plan goes on with them")
	case control.EndEarlyDeath:
		return c.notice(a.Item, "early/"+at.ID, a.Text+" without writing any task; a new planner is started"+tails([]control.Attempt{at}))
	case control.EndFailed:
		return c.plannerStopped(st, a, at)
	}
	return nil
}

// plannerStopped tells the operator that a second planner death without a
// task stopped the plan's coordination.
func (c *Coordinator) plannerStopped(st control.State, a reconcile.Action, at control.Attempt) error {
	deaths := earlyAttempts(st, at)
	if len(deaths) > 2 {
		deaths = deaths[len(deaths)-2:]
	}
	return c.notice(a.Item, "failed/"+at.ID, a.Text+" without writing any task, for the second time; coordination of the plan stopped. "+
		"Run the plan again to start a new planner"+tails(deaths))
}
