package run

import (
	"strings"
	"time"

	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/tracker"
)

// The coordinator's notes on a task's sessions, kept in its note directory
// whichever host the session runs on.
const (
	// WaitingSince is when the session was first seen waiting for input.
	WaitingSince = "waiting-since"
	// StallNoted is the session start, or the wait, the coordinator last
	// raised a stall for on the task.
	StallNoted = "stall-noted"
	// StartedOn, followed by a host's name, is when the coordinator last
	// started the task's session on that host.
	StartedOn = "started-on-"
)

// sessions reads the newest run of each of v's tasks, and of the feature's
// planner, from the hosts' replies in v.Fleet, and keeps each one's
// waiting-since note true to the state its window shows at v.Now.
func sessions(w World, v View) (map[tracker.Ref]Session, error) {
	refs := []tracker.Ref{}
	if !v.TaskOnly {
		refs = append(refs, v.Feature.Ref)
	}
	for _, c := range v.Feature.Tasks {
		refs = append(refs, c.Ref)
	}
	out := map[tracker.Ref]Session{}
	for _, ref := range refs {
		s, ok, err := readSession(w, ref, v.Fleet, v.Rows[ref].Host, v.Now)
		if err != nil {
			return nil, err
		}
		if ok {
			out[ref] = s
		}
	}
	return out, nil
}

// readSession finds ref's session on the host its claim names, else the
// host the coordinator last started it on. When that host could not be
// read, the session is unknown. Otherwise it is the newest run of its name
// on the hosts that answered.
func readSession(w World, ref tracker.Ref, fleet Fleet, claimHost string, now time.Time) (Session, bool, error) {
	repo, err := session.ParseRepo(ref.Repo)
	if err != nil {
		return Session{}, false, err
	}
	name := session.TaskName(repo, ref.Number)
	dir, err := w.NoteDir(ref)
	if err != nil {
		return Session{}, false, err
	}
	host := claimHost
	if _, ok := fleet.host(host); !ok {
		if host, err = startedOn(dir, fleet); err != nil {
			return Session{}, false, err
		}
	}
	h, known := fleet.host(host)
	if known && h.Unreachable != "" {
		return Session{Name: name, Host: host, Unknown: true}, true, nil
	}
	s, run, ok := newestRun(fleet, name)
	if !ok {
		// A run.json the host could not read may be this session's.
		unknown := known && h.Status.RunsError != ""
		return Session{Name: name, Host: host, Unknown: unknown}, unknown, nil
	}
	waitKnown := see(&s, run, name)
	if err := keepWaiting(dir, &s, waitKnown, now); err != nil {
		return Session{}, false, err
	}
	s.StallNoted, _, err = session.ReadNote(dir, StallNoted)
	return s, true, err
}

// startedOn is the host the coordinator last started a session on, from
// its notes in dir, or "" when it has started none.
func startedOn(dir string, fleet Fleet) (string, error) {
	host, last := "", time.Time{}
	for _, h := range fleet.Hosts {
		at, ok, err := session.ReadNote(dir, StartedOn+h.Name)
		if err != nil {
			return "", err
		}
		if ok && at.After(last) {
			host, last = h.Name, at
		}
	}
	return host, nil
}

// newestRun finds the newest run named name on the hosts that answered, and
// the host it is on.
func newestRun(fleet Fleet, name string) (Session, HostLoad, bool) {
	var best hostops.Run
	var on HostLoad
	found := false
	for _, h := range fleet.Hosts {
		if h.Unreachable != "" {
			continue
		}
		for _, r := range h.Status.Runs {
			if r.Name == name && (!found || r.Record.Spawned.After(best.Record.Spawned)) {
				best, on, found = r, h, true
			}
		}
	}
	if !found {
		return Session{}, HostLoad{}, false
	}
	_, id, _ := strings.Cut(best.Record.Tmux, ":")
	return Session{Name: name, Host: on.Name, WindowID: id, Mode: best.Record.Mode, Spawned: best.Record.Spawned}, on, true
}

// see reads the session's window, and the state status gives it, from its
// host's status. It reports whether the session's state is known: a live
// window whose state is unknown leaves it unknown.
func see(s *Session, h HostLoad, name string) bool {
	known := true
	for _, win := range h.Status.Windows {
		if win.ID == s.WindowID && win.Name == name {
			s.Live, s.DeadWindow, s.Screen = win.Alive, !win.Alive, win.Screen
			s.Limited = win.Alive && win.State == session.StateLimited
			s.ResetsAt = win.ResetsAt
			s.Waiting = win.Alive && win.State == session.StateWaiting
			known = !win.Alive || win.State != ""
		}
	}
	return known
}

// keepWaiting writes the time a session is first seen waiting, and clears it
// when the session works again. When the session's state is unknown, the
// note is left as it is.
func keepWaiting(dir string, s *Session, known bool, now time.Time) error {
	if !known {
		return nil
	}
	if !s.Live || !s.Waiting {
		return session.ClearNote(dir, WaitingSince)
	}
	since, ok, err := session.ReadNote(dir, WaitingSince)
	if err != nil {
		return err
	}
	if !ok {
		since = now
		if err := session.WriteNote(dir, WaitingSince, now); err != nil {
			return err
		}
	}
	s.WaitingSince = since
	return nil
}
