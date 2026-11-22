package hostops

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/session"
)

// ScreenLines is how much of a window's screen status returns.
const ScreenLines = 20

// PaneTailLines and PaneTailAge bound the pane log tail status returns: how
// many lines, and how recently the log was last written. The newest run of
// every session name is listed, so the tail is limited to a run that just
// ended.
const (
	PaneTailLines = 20
	PaneTailAge   = time.Hour
)

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
	// TmuxCwdGone is set when the tmux server's working directory no longer
	// exists: the host is unready, and placement skips it. TmuxCwdError says
	// why the directory could not be read, when it could not.
	TmuxCwdGone  *TmuxCwdGone `json:"tmux_cwd_gone,omitempty"`
	TmuxCwdError string       `json:"tmux_cwd_error,omitempty"`
	// TmuxUnit is set when the tmux server runs inside a systemd service,
	// so stopping or restarting that service ends every window. It does not
	// make the host unready. TmuxUnitError says why the server's cgroup could
	// not be read, when it could not.
	TmuxUnit      *TmuxUnit `json:"tmux_unit,omitempty"`
	TmuxUnitError string    `json:"tmux_unit_error,omitempty"`
	// TmuxStart is when the session tmux server started, in epoch seconds.
	// TmuxKnown is false when no tmux server runs: a restarted server has a
	// new start time, and a host with none running has no time to compare.
	TmuxStart int64 `json:"tmux_start,omitempty"`
	TmuxKnown bool  `json:"tmux_known"`
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
	// Dialog is true for a waiting session held at a dialog that typed
	// input would answer: nothing may be typed into it.
	Dialog bool `json:"dialog,omitempty"`
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
	// PaneTail is the last PaneTailLines lines of the run's pane log, for a
	// run with no live window whose log was written within PaneTailAge.
	PaneTail string `json:"pane_tail,omitempty"`
	// Limit is set for a run with no live window whose harness marker says
	// it stopped at a usage limit: it died there.
	Limit *RunLimit `json:"limit,omitempty"`
	// PaneKey is the public key, in hex, of a server run's pane key.
	PaneKey string `json:"pane_key,omitempty"`
}

// RunLimit is the usage limit a run with no live window stopped at.
type RunLimit struct {
	// ResetsAt is the reset in epoch seconds, or zero when unknown.
	ResetsAt int64 `json:"resets_at,omitempty"`
}

// ReadStatus reads this host's windows and the state of each, each harness's
// quota and sign-in, newest runs and free slots, heavy and all, and the
// screen of every window whose session is not working, and when its tmux
// server started. A tmux that cannot list its windows, or say when it
// started, fails the op: nothing it would say is known.
func ReadStatus(d Deps) (Status, error) {
	_, host, err := openHost(d.Home)
	if err != nil {
		return Status{}, err
	}
	adapters, err := d.adapters()
	if err != nil {
		return Status{}, err
	}
	windows, err := d.Tmux.Windows()
	if err != nil {
		return Status{}, err
	}
	start, known, err := d.Tmux.ServerStart()
	if err != nil {
		return Status{}, err
	}
	codes, err := d.Tmux.ExitCodes()
	if err != nil {
		return Status{}, err
	}
	s := Status{Slots: host.Slots(), HeavySlots: host.HeavySlots(), Windows: []Window{}, Runs: []Run{}, TmuxStart: start, TmuxKnown: known}
	runs, err := session.LatestRuns(d.Home)
	if err != nil {
		s.RunsError = err.Error()
	}
	states := s.observe(adapters, host, windows, runs)
	s.readServer(d)
	for _, w := range windows {
		out := Window{Name: w.Name, ID: w.ID, Alive: !w.Dead}
		if code, ok := codes[w.ID]; ok && w.Dead {
			out.ExitCode = &code
		}
		if r, ok := runs[w.Name]; ok {
			out.Harness = r.Record.Harness
			if st, ok := states[w.Name]; ok && out.Alive {
				out.State, out.WaitingFor, out.Dialog, out.Background, out.ResetsAt = st.State, st.WaitingFor, st.Dialog, st.Background, st.ResetsAt
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
	s.Runs = d.listRuns(adapters, runs, s.Windows)
	return s, nil
}

// listRuns is the newest run of each session name, sorted by name. A run
// with no live window carries the tail of its pane log, and the usage limit
// its harness's marker says it stopped at.
func (d Deps) listRuns(a harness.Adapters, runs map[string]session.Run, windows []Window) []Run {
	alive := map[string]bool{}
	for _, w := range windows {
		alive[w.Name] = alive[w.Name] || w.Alive
	}
	out := []Run{}
	for name, r := range runs {
		run := Run{Name: name, Dir: r.Dir, Record: r.Record}
		if r.Record.Control {
			run.PaneKey, _ = session.PanePublicKey(r.Dir) // a run spawned before pane keys has none
		}
		if !alive[name] {
			run.PaneTail = d.paneTail(r.Dir)
			run.Limit = runLimit(a, r)
		}
		out = append(out, run)
	}
	slices.SortFunc(out, func(a, b Run) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// runLimit is the usage limit r's harness marker records, or nil.
func runLimit(a harness.Adapters, r session.Run) *RunLimit {
	h, ok := a.Get(r.Record.Harness)
	if !ok {
		return nil
	}
	m, ok := h.(harness.LimitMarker)
	if !ok {
		return nil
	}
	if reset, limited := m.RunLimit(r.Dir); limited {
		return &RunLimit{ResetsAt: reset}
	}
	return nil
}

// observe has each adapter read its harness on this host, given the newest
// run of every live window that names it, and records each one's quota,
// sign-in and errors. It returns the windows' states by name.
func (s *Status) observe(a harness.Adapters, host hosts.Host, windows []session.WindowInfo, runs map[string]session.Run) map[string]harness.State {
	states := map[string]harness.State{}
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
