package run

import (
	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/session"
)

// status is what a host's status op says: its windows, with the state and
// the screen of each whose session is not working, its live Claude Code
// sessions and its newest runs.
func (w *world) status(h fakeHost) hostops.Status {
	st := hostops.Status{Slots: h.slots, FreeSlots: max(0, h.slots-len(w.live(h.name))), HeavySlots: h.heavySlots(), AgentsError: h.agentsDown}
	st.FreeHeavySlots = min(max(0, h.heavySlots()-w.liveHeavy(h.name)), st.FreeSlots)
	for _, e := range w.execs {
		if e.host != h.name || e.window.ID == "" {
			continue
		}
		win := hostops.Window{Name: e.window.Name, ID: e.window.ID, Alive: !e.window.Dead, Harness: e.harness}
		// A Codex window's state is its state file's, whatever Claude Code's
		// list says; Claude Code's list holds Claude sessions only.
		switch agent := (session.Agent{Name: e.window.Name, Status: e.status}); {
		case e.window.Dead:
		case e.harness == hostops.Codex:
			win.State = session.AgentState(agent)
		case h.agentsDown == "":
			st.Agents = append(st.Agents, agent)
			win.State = session.AgentState(agent)
		}
		if e.limited && win.Alive {
			win.State, win.ResetsAt = session.StateLimited, e.resetsAt
		}
		if e.resetsAt != 0 && win.Alive && win.State != session.StateWorking {
			win.ResetsAt = e.resetsAt
		}
		if win.State != session.StateWorking {
			win.Screen = "Do you want to proceed?\n"
		}
		st.Windows = append(st.Windows, win)
	}
	runs, err := session.LatestRuns(w.hostHome(h.name))
	if err != nil {
		w.t.Fatal(err)
	}
	for name, r := range runs {
		st.Runs = append(st.Runs, hostops.Run{Name: name, Dir: r.Dir, Record: r.Record})
	}
	return st
}
