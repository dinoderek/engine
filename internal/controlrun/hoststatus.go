package controlrun

import (
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/reconcile"
)

// HostBoard keeps the fleet as the coordinator's latest pass read it, for the
// host-status op. A request reads only the board, never a host. It starts
// empty with the server and holds no more than one pass.
type HostBoard struct {
	Now func() time.Time

	mu   sync.Mutex
	last control.HostStatus
}

// Record replaces the board with a pass's fleet reads: each host's status,
// the attempts running in its windows and its readiness. A nil board or
// readiness records without it.
func (b *HostBoard) Record(st control.State, fleet reconcile.Fleet, ready *Readiness) {
	if b == nil {
		return
	}
	now := time.Now()
	if b.Now != nil {
		now = b.Now()
	}
	snap := control.HostStatus{At: now.UTC(), Hosts: make([]control.HostRow, 0, len(fleet.Hosts))}
	attempts := activeAttempts(st)
	for _, h := range fleet.Hosts {
		row := hostRow(st, attempts, h)
		if ready != nil {
			row.Readiness = ready.Report(h.Name)
		}
		snap.Hosts = append(snap.Hosts, row)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.last = snap
}

// Status is the latest pass's fleet; before any pass it has no hosts.
func (b *HostBoard) Status() control.HostStatus {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.last
	if out.Hosts == nil {
		out.Hosts = []control.HostRow{}
	}
	return out
}

func hostRow(st control.State, attempts []control.Attempt, h reconcile.HostLoad) control.HostRow {
	row := control.HostRow{Name: h.Name, Windows: []control.HostWindow{}}
	if h.Unreachable != "" {
		row.Unreachable = h.Unreachable
		return row
	}
	s := h.Status
	row.Slots, row.FreeSlots, row.HeavySlots, row.FreeHeavySlots = s.Slots, s.FreeSlots, s.HeavySlots, s.FreeHeavySlots
	row.Unready, row.SignedOut = h.Unready(), s.SignedOut
	harness := map[string]string{}
	for _, r := range s.Runs {
		harness[r.Name] = r.Record.Harness
	}
	for _, w := range s.Windows {
		row.Windows = append(row.Windows, hostWindow(st, attempts, h.Name, w, harness[w.Name]))
	}
	for name, q := range s.Quota {
		for window, u := range q.Windows {
			row.Quota = append(row.Quota, control.HostQuota{Harness: cmpHarness(name, q.Harness), Window: window, Used: u.Used, ResetsAt: u.ResetsAt})
		}
	}
	slices.SortFunc(row.Quota, func(x, y control.HostQuota) int {
		return strings.Compare(x.Harness+"/"+x.Window, y.Harness+"/"+y.Window)
	})
	return row
}

// cmpHarness names a quota's harness: the reading's own, else its key.
func cmpHarness(key, own string) string {
	if own != "" {
		return own
	}
	return key
}

// hostWindow is a window with the active attempt that runs in it: the one on
// this host with the window's id and its session's name, since tmux reuses
// window ids after a restart.
func hostWindow(st control.State, attempts []control.Attempt, host string, w hostops.Window, runHarness string) control.HostWindow {
	out := control.HostWindow{Name: w.Name, Alive: w.Alive, Harness: w.Harness, State: w.State}
	if out.Harness == "" {
		out.Harness = runHarness
	}
	for _, at := range attempts {
		item := st.Items[at.Item]
		if at.Host == host && at.Window == w.ID && w.Name == sessionName(st.Items[item.Repo].Slug, control.Number(item.ID)) {
			out.Item, out.Attempt = item.ID, at.ID
			break
		}
	}
	return out
}

// Report is the host's readiness for each repo asked about, by repo name.
func (r *Readiness) Report(host string) []control.HostReadiness {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []control.HostReadiness
	for key, e := range r.entries {
		if key.host != host {
			continue
		}
		row := control.HostReadiness{Repo: e.slug, State: "unknown"}
		switch {
		case e.err != "":
			row.State, row.Detail = "unready", "its readiness probe could not run: "+e.err
		case e.result != nil && len(e.result.Failed()) > 0:
			row.State, row.Detail = "unready", unready(*e.result)
		case e.result != nil:
			row.State = "ready"
		}
		out = append(out, row)
	}
	slices.SortFunc(out, func(x, y control.HostReadiness) int { return strings.Compare(x.Repo, y.Repo) })
	return out
}
