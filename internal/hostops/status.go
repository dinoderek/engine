package hostops

import (
	"slices"
	"strings"

	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/session"
)

// ScreenLines is how much of a window's screen status returns.
const ScreenLines = 20

// Status is status's reply: what this host's sessions are doing now.
type Status struct {
	// Slots is how many sessions the host runs at once, and FreeSlots how
	// many more it can start: only a live window with a run recorded under
	// its name holds one.
	Slots     int      `json:"slots"`
	FreeSlots int      `json:"free_slots"`
	Windows   []Window `json:"windows"`
	// Agents are the live Claude Code sessions. When AgentsError says why
	// Claude Code could not list them, what they would show is unknown.
	Agents      []session.Agent `json:"agents"`
	AgentsError string          `json:"agents_error,omitempty"`
	// Runs is the newest run of each session name, by name. RunsError
	// names the run.json files that could not be read: those runs are
	// unknown.
	Runs      []Run  `json:"runs"`
	RunsError string `json:"runs_error,omitempty"`
}

// Window is a window of the tmux session engine.
type Window struct {
	Name  string `json:"name"`
	ID    string `json:"id"`
	Alive bool   `json:"alive"`
	// ExitCode is the exit code of a window whose command has exited, unless
	// a signal ended it.
	ExitCode *int `json:"exit_code,omitempty"`
	// Screen is the last ScreenLines lines of a window whose session is not
	// busy.
	Screen string `json:"screen,omitempty"`
}

// Run is a run directory and its run.json.
type Run struct {
	Name   string         `json:"name"`
	Dir    string         `json:"dir"`
	Record session.Record `json:"run"`
}

// ReadStatus reads this host's windows, live Claude Code sessions, newest
// runs and free slots, and the screen of every window whose session is not
// busy. A tmux that cannot list its windows fails the op: nothing it would
// say is known.
func ReadStatus(d Deps) (Status, error) {
	_, host, err := openHost(d.Home)
	if err != nil {
		return Status{}, err
	}
	windows, err := d.Tmux.Windows()
	if err != nil {
		return Status{}, err
	}
	codes, err := d.Tmux.ExitCodes()
	if err != nil {
		return Status{}, err
	}
	s := Status{Slots: host.Slots(), Windows: []Window{}, Agents: []session.Agent{}, Runs: []Run{}}
	if host.Runs(Harness) {
		s.listAgents(d, host)
	}
	runs, err := session.LatestRuns(d.Home)
	if err != nil {
		s.RunsError = err.Error()
	}
	live := 0
	for _, w := range windows {
		out := Window{Name: w.Name, ID: w.ID, Alive: !w.Dead}
		if code, ok := codes[w.ID]; ok && w.Dead {
			out.ExitCode = &code
		}
		if !s.busy(w.Name) {
			// A window can close between the list and the capture.
			out.Screen, _ = d.Tmux.Capture(w.ID, ScreenLines)
		}
		if session.HoldsSlot(w, runs) {
			live++
		}
		s.Windows = append(s.Windows, out)
	}
	s.FreeSlots = max(s.Slots-live, 0)
	for name, r := range runs {
		s.Runs = append(s.Runs, Run{Name: name, Dir: r.Dir, Record: r.Record})
	}
	slices.SortFunc(s.Runs, func(a, b Run) int { return strings.Compare(a.Name, b.Name) })
	return s, nil
}

func (s *Status) listAgents(d Deps, host hosts.Host) {
	bin, err := host.BinPath(Harness, d.Home)
	if err == nil {
		var agents []session.Agent
		if agents, err = session.ListAgents(d.Exec, bin); err == nil && agents != nil {
			s.Agents = agents
		}
	}
	if err != nil {
		s.AgentsError = err.Error()
	}
}

// busy reports whether Claude Code lists the session name as working.
func (s *Status) busy(name string) bool {
	return slices.ContainsFunc(s.Agents, func(a session.Agent) bool { return a.Name == name && a.Status == session.AgentBusy })
}
