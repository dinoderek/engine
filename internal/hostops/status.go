package hostops

import (
	"context"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/session"
)

// ScreenLines is how much of a window's screen status returns.
const ScreenLines = 20

// Status is status's reply: what this host's sessions are doing now.
type Status struct {
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
	// HarnessErrors says, by harness, why its sessions' states are unknown.
	HarnessErrors map[string]string `json:"harness_errors,omitempty"`
	// Runs is the newest run of each session name, by name. RunsError
	// names the run.json files that could not be read: those runs are
	// unknown.
	Runs      []Run  `json:"runs"`
	RunsError string `json:"runs_error,omitempty"`
	// SignedOut names the harnesses this host runs whose own CLI says they
	// are signed out. A harness whose check cannot run is not listed.
	SignedOut []string `json:"signed_out,omitempty"`
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
	// Background is true for a working session whose last turn has ended:
	// only a background job keeps it busy, and it takes typed input.
	Background bool `json:"background,omitempty"`
	// ResetsAt is the usage limit reset, in epoch seconds, or zero if unknown.
	ResetsAt int64 `json:"resets_at,omitempty"`
	// Screen is the last ScreenLines lines of a window whose session is not
	// working.
	Screen string `json:"screen,omitempty"`
	// Denial is the newest entry of a waiting session's permissions.jsonl,
	// when that entry is a denial.
	Denial *Denial `json:"denial,omitempty"`
}

// Run is a run directory and its run.json.
type Run struct {
	Name   string         `json:"name"`
	Dir    string         `json:"dir"`
	Record session.Record `json:"run"`
}

// ReadStatus reads this host's windows and the state of each, each harness's
// quota and sign-in, newest runs and free slots, heavy and all, and the
// screen of every window whose session is not working. A tmux that cannot
// list its windows fails the op: nothing it would say is known.
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
	s := Status{Slots: host.Slots(), HeavySlots: host.HeavySlots(), Windows: []Window{}, Runs: []Run{}}
	runs, err := session.LatestRuns(d.Home)
	if err != nil {
		s.RunsError = err.Error()
	}
	states := s.observe(d, host, windows, runs)
	for _, w := range windows {
		out := Window{Name: w.Name, ID: w.ID, Alive: !w.Dead}
		if code, ok := codes[w.ID]; ok && w.Dead {
			out.ExitCode = &code
		}
		if r, ok := runs[w.Name]; ok {
			out.Harness = r.Record.Harness
			if st, ok := states[w.Name]; ok && out.Alive {
				out.State, out.WaitingFor, out.Background, out.ResetsAt = st.State, st.WaitingFor, st.Background, st.ResetsAt
			}
			if out.State == session.StateWaiting {
				out.Denial = LastDenial(filepath.Join(r.Dir, session.PermissionLog))
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

// observe has each adapter read its harness on this host, given the newest
// run of every live window that names it, and records each one's quota,
// sign-in and errors. It returns the windows' states by name.
func (s *Status) observe(d Deps, host hosts.Host, windows []session.WindowInfo, runs map[string]session.Run) map[string]harness.State {
	states := map[string]harness.State{}
	a := d.adapters()
	if a == nil {
		return states
	}
	for _, name := range a.Names() {
		h, _ := a.Get(name)
		mine := map[string]session.Run{}
		for _, w := range windows {
			if r, ok := runs[w.Name]; ok && !w.Dead && r.Record.Harness == name {
				mine[w.Name] = r
			}
		}
		snap := h.Observe(context.Background(), host, mine)
		if snap.Quota != nil {
			if s.Quota == nil {
				s.Quota = map[string]session.Quota{}
			}
			s.Quota[name] = *snap.Quota
		}
		if snap.SignedOut {
			s.SignedOut = append(s.SignedOut, name)
		}
		if snap.Error != "" {
			if s.HarnessErrors == nil {
				s.HarnessErrors = map[string]string{}
			}
			s.HarnessErrors[name] = snap.Error
		}
		for w, st := range snap.Sessions {
			states[w] = st
		}
	}
	return states
}
