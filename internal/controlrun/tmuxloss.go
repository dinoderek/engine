package controlrun

import (
	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/reconcile"
)

// sawTmux records each host's tmux server as this pass read it, which marks
// a host-wide loss before any plan decides on its attempts. A host that
// could not be read shows nothing of its server.
func (c *Coordinator) sawTmux(f reconcile.Fleet) {
	for _, h := range f.Hosts {
		if h.Unreachable != "" {
			continue
		}
		read := control.TmuxRead{Known: h.Status.TmuxKnown, Start: h.Status.TmuxStart}
		if _, err := c.Service.SawTmux(h.Name, read, windowAlive(h)); err != nil {
			c.log("tmux of %s: %v", h.Name, err)
		}
	}
}

// sawReach records whether this pass could read each host, which asks the
// operator about one unreachable for long, runs their answer, and settles
// the question once the host answers (control.Service.SawReach).
func (c *Coordinator) sawReach(f reconcile.Fleet) {
	for _, h := range f.Hosts {
		if err := c.Service.SawReach(h.Name, h.Unreachable == "", windowAlive(h)); err != nil {
			c.log("reach of %s: %v", h.Name, err)
		}
	}
}

// windowAlive reports whether an attempt's window runs on host h, as this
// pass read it.
func windowAlive(h reconcile.HostLoad) func(*control.Tx, control.Attempt) bool {
	return func(tx *control.Tx, at control.Attempt) bool {
		item, _ := tx.Item(at.Item)
		repo, _ := tx.Item(item.Repo)
		w, ok := matchWindow(at, h.Status, sessionName(repo.Slug, control.Number(item.ID)))
		return ok && w.Alive
	}
}
