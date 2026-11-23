package controlrun

import (
	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/reconcile"
)

// sawLimits settles, once a pass, the fleet questions about the task
// sessions of coordinated plans that wait on a usage limit: it asks about
// those that could move, runs the operator's answers and resolves questions
// whose sessions are no longer limited, on each host read in full
// (control.Service.SawLimits). It runs before the plans decide, so a session
// a question covers sends no far-reset notice.
func (c *Coordinator) sawLimits(fleet reconcile.Fleet) {
	st, err := c.Service.Store.Snapshot()
	if err != nil {
		c.log("limits: %v", err)
		return
	}
	f := fleet
	f.Now, f.Limits, f.Hosts = c.now(), control.HarnessLimits(st.LegacyTx()), pausedHosts(st, fleet.Hosts)
	var tasks []reconcile.Item
	for id, root := range st.Items {
		if !root.Coordinating {
			continue
		}
		if root.Kind == "task" {
			tasks = append(tasks, withSession(st, root, f))
		}
		for _, t := range Tasks(st, id) {
			tasks = append(tasks, withSession(st, t, f))
		}
	}
	reads := reconcile.LimitChoices(f, tasks)
	for _, h := range f.Hosts {
		var mine []control.LimitRead
		for _, r := range reads {
			if r.Host == h.Name {
				mine = append(mine, r)
			}
		}
		if h.Unreachable != "" || h.Status.RunsError != "" || len(mine) == 0 && len(st.Hosts[h.Name].Limits) == 0 {
			continue
		}
		if err := c.Service.SawLimits(h.Name, mine); err != nil {
			c.log("limits of %s: %v", h.Name, err)
		}
	}
}
