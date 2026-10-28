package hostops

import (
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/session"
)

// ScreenLines is how much of a window's screen status returns.
const ScreenLines = 20

// Status is status's reply: what this host's sessions are doing now.
type Status struct {
	codexTurns map[string]json.RawMessage

	Quota map[string]session.Quota `json:"quota,omitempty"`
	// Slots is how many sessions the host runs at once, and FreeSlots how
	// many more it can start: only a live window with a run recorded under
	// its name holds one. HeavySlots is how many of them may be heavy, and
	// FreeHeavySlots how many more heavy sessions it can start, never more
	// than FreeSlots.
	Slots          int      `json:"slots"`
	FreeSlots      int      `json:"free_slots"`
	HeavySlots     int      `json:"heavy_slots"`
	FreeHeavySlots int      `json:"free_heavy_slots"`
	Windows        []Window `json:"windows"`
	// Agents are the live Claude Code sessions, so a caller can find one
	// started by hand; a window's state already says what Claude Code does.
	// When AgentsError says why Claude Code could not list them, what they
	// would show is unknown.
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
	// Harness is the harness the window's newest run launched, and State
	// what its session is doing, from that harness's probe: working, waiting,
	// limited, or unknown (left out). A dead window, or one with no run, is unknown.
	Harness string `json:"harness,omitempty"`
	State   string `json:"state,omitempty"`
	// WaitingFor says what a waiting session needs, when its harness says.
	WaitingFor string `json:"waiting_for,omitempty"`
	// ResetsAt is the usage limit reset, in epoch seconds, or zero if unknown.
	ResetsAt int64 `json:"resets_at,omitempty"`
	// Screen is the last ScreenLines lines of a window whose session is not
	// working.
	Screen string `json:"screen,omitempty"`
}

// Run is a run directory and its run.json.
type Run struct {
	Name   string         `json:"name"`
	Dir    string         `json:"dir"`
	Record session.Record `json:"run"`
}

// ReadStatus reads this host's windows and the state of each, live Claude
// Code sessions, newest runs and free slots, heavy and all, and the screen
// of every window whose session is not working. A tmux that cannot list its
// windows fails the op: nothing it would say is known.
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
	now := time.Now
	if d.Now != nil {
		now = d.Now
	}
	s := Status{Quota: session.ReadQuota(d.Home, now()), Slots: host.Slots(), HeavySlots: host.HeavySlots(), Windows: []Window{}, Agents: []session.Agent{}, Runs: []Run{}}
	s.readCodexQuota(d, host, now)
	if host.Runs(Claude) {
		s.listAgents(d, host)
	}
	runs, err := session.LatestRuns(d.Home)
	if err != nil {
		s.RunsError = err.Error()
	}
	s.codexTurns = codexTurnReadings(d, host, windows, runs)
	for _, w := range windows {
		out := Window{Name: w.Name, ID: w.ID, Alive: !w.Dead}
		if code, ok := codes[w.ID]; ok && w.Dead {
			out.ExitCode = &code
		}
		if r, ok := runs[w.Name]; ok {
			out.Harness = r.Record.Harness
			if out.Alive {
				s.probe(&out, r)
			}
		}
		if out.State != session.StateWorking {
			// A window can close between the list and the capture.
			out.Screen, _ = d.Tmux.Capture(w.ID, ScreenLines)
		}
		s.Windows = append(s.Windows, out)
	}
	live, heavy := session.SlotsHeld(windows, runs)
	s.FreeSlots = max(s.Slots-live, 0)
	s.FreeHeavySlots = min(max(s.HeavySlots-heavy, 0), s.FreeSlots)
	for name, r := range runs {
		s.Runs = append(s.Runs, Run{Name: name, Dir: r.Dir, Record: r.Record})
	}
	slices.SortFunc(s.Runs, func(a, b Run) int { return strings.Compare(a.Name, b.Name) })
	return s, nil
}

func (s *Status) listAgents(d Deps, host hosts.Host) {
	bin, err := host.BinPath(Claude, d.Home)
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

// probe sets a live window's state from the probe of the harness its
// newest run launched. A harness with no probe leaves it unknown.
func (s *Status) probe(w *Window, r session.Run) {
	switch r.Record.Harness {
	case Claude:
		if s.AgentsError != "" {
			return
		}
		for _, a := range s.Agents {
			if a.Name == w.Name {
				w.State = session.AgentState(a)
				if w.State == session.StateWaiting {
					w.WaitingFor = a.WaitingFor
				}
				break
			}
		}
		if l, ok := session.ReadLimit(r.Dir); ok && w.State != session.StateWorking {
			w.State, w.ResetsAt, w.WaitingFor = session.StateLimited, l.ResetsAt, ""
		}
	case Codex:
		s.probeCodex(w, r)
	}
}
