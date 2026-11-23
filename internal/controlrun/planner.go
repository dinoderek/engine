package controlrun

import (
	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/reconcile"
)

// plannerEnded tells the operator how a dead planner's attempt at ended: a
// planner that wrote tasks leaves the plan to go on with them; one that
// wrote none is started again once, and a second such death stops
// coordination, with both deaths' pane log tails.
func (c *Coordinator) plannerEnded(history []control.Attempt, a reconcile.Action, at control.Attempt) error {
	switch at.Reason {
	case control.EndVanished:
		return c.notice(a.Item, "vanished/"+at.ID, a.Text+" after writing tasks; the plan goes on with them")
	case control.EndEarlyDeath:
		return c.notice(a.Item, "early/"+at.ID, a.Text+" without writing any task; a new planner is started"+tails([]control.Attempt{at}))
	case control.EndFailed:
		return c.plannerStopped(history, a, at)
	}
	return nil
}

// plannerStopped tells the operator that a second planner death without a
// task stopped the plan's coordination.
func (c *Coordinator) plannerStopped(history []control.Attempt, a reconcile.Action, at control.Attempt) error {
	deaths := earlyAttempts(history, at)
	if len(deaths) > 2 {
		deaths = deaths[len(deaths)-2:]
	}
	return c.notice(a.Item, "failed/"+at.ID, a.Text+" without writing any task, for the second time; coordination of the plan stopped. "+
		"Run the plan again to start a new planner"+tails(deaths))
}

// plannerAtLimit tells the operator when a new planner starts after one died
// at a usage limit, unless the planner before it died at a limit too: the
// operator was told then, so a replacement that meets the limit again is
// quiet.
func (c *Coordinator) plannerAtLimit(history []control.Attempt, a reconcile.Action, at control.Attempt) error {
	var prev control.Attempt
	for _, o := range history {
		if control.Number(o.ID) < control.Number(at.ID) && control.Number(o.ID) > control.Number(prev.ID) {
			prev = o
		}
	}
	if prev.Limited {
		return nil
	}
	return c.notice(a.Item, "limit-death/"+at.ID, a.Text+"; a new planner is started "+limitWhen(at))
}
