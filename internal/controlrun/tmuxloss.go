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
		alive := func(st *control.State, at control.Attempt) bool {
			item := st.Items[at.Item]
			w, ok := matchWindow(at, h.Status, sessionName(st.Items[item.Repo].Slug, control.Number(item.ID)))
			return ok && w.Alive
		}
		if _, err := c.Service.SawTmux(h.Name, read, alive); err != nil {
			c.log("tmux of %s: %v", h.Name, err)
		}
	}
}
