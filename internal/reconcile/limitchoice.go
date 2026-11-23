package reconcile

import (
	"cmp"
	"slices"
	"time"

	"github.com/dinoderek/engine/internal/control"
)

// LimitWaitUnder is how near a known reset must be for waiting to be the
// recommended answer to a limit question.
const LimitWaitUnder = 2 * time.Hour

// LimitChoices are what the live task sessions at a usage limit offer the
// operator this pass, one per host × harness that has any, in host then
// harness order (control.Service.SawLimits asks about them).
//
// A session counts while its task is open, neither stopped, held nor at a
// hard stop, and its window is live and limited, or waiting with a limit
// reset. Room is a host × harness that could start the task now, other than
// a pair with such a session: for one task, any other pair it may run on,
// which becomes the move; for several on one host × harness, another host
// for at least one of them, as `engine host move` sends them anywhere but
// their host. A task pinned to a harness keeps it.
func LimitChoices(f Fleet, tasks []Item) []control.LimitRead {
	groups := map[pair][]Item{}
	for _, t := range tasks {
		if movableAtLimit(t) {
			p := pair{t.Current.Host, t.Current.Harness}
			groups[p] = append(groups[p], t)
		}
	}
	var out []control.LimitRead
	for p, ts := range groups {
		slices.SortFunc(ts, func(a, b Item) int { return control.Number(a.ID) - control.Number(b.ID) })
		r := control.LimitRead{Host: p.host, Harness: p.harness, ResetsAt: latestReset(ts)}
		for _, t := range ts {
			r.Tasks = append(r.Tasks, t.ID)
		}
		if len(ts) == 1 {
			r.MoveHost, r.MoveHarness, r.Room = f.room(ts[0], false, groups)
		} else {
			r.Room = slices.ContainsFunc(ts, func(t Item) bool { _, _, ok := f.room(t, true, groups); return ok })
		}
		r.Wait = r.ResetsAt > 0 && time.Unix(r.ResetsAt, 0).Before(f.Now.Add(LimitWaitUnder))
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b control.LimitRead) int {
		return cmp.Or(cmp.Compare(a.Host, b.Host), cmp.Compare(a.Harness, b.Harness))
	})
	return out
}

// movableAtLimit reports whether t is a task whose live session waits on a
// usage limit, and that a move could take: open, running, and at no hard
// stop.
func movableAtLimit(t Item) bool {
	s := t.Session
	return t.Kind == "task" && t.open() && !t.Stopped && !t.Held && t.state() != "needs-human" && t.Question == 0 &&
		t.Current.State != "ended" && t.Current.State != "dispatching" &&
		s != nil && s.Live && !s.Unknown && (s.Limited || s.Waiting && s.ResetsAt > 0)
}

// latestReset is the latest of the sessions' resets, zero when any is
// unknown.
func latestReset(ts []Item) int64 {
	var latest int64
	for _, t := range ts {
		if t.Session.ResetsAt <= 0 {
			return 0
		}
		latest = max(latest, t.Session.ResetsAt)
	}
	return latest
}

// room is the host × harness that could start limited task t now, as
// placement would pick it: not t's own pair, nor one with a limited
// session, nor, when offHost, any pair on t's host.
func (f Fleet) room(t Item, offHost bool, limited map[pair][]Item) (host, harness string, ok bool) {
	w := want(t)
	if offHost {
		w.Avoid = t.Current.Host
	}
	free := map[string]slots{}
	for _, h := range f.Hosts {
		free[h.Name] = h.free()
	}
	running := f.Running()
	names, err := f.harnesses(w, running)
	if err != nil {
		return "", "", false
	}
	for _, name := range names {
		// Moving within its own harness frees the slot it starts in.
		if name != t.Current.Harness && running[name] >= f.Caps[name] {
			continue
		}
		fits, _, _ := f.fits(w, name, map[pair]bool{})
		fits = slices.DeleteFunc(fits, func(h HostLoad) bool {
			_, busy := limited[pair{h.Name, name}]
			return busy || h.Name == t.Current.Host && name == t.Current.Harness
		})
		if best, found := pick(fits, free, f.heavy(w)); found {
			return best.Name, name, true
		}
	}
	return "", "", false
}
