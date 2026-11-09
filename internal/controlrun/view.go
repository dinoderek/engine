package controlrun

import (
	"slices"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/lint"
	"github.com/dinoderek/engine/internal/reconcile"
	"github.com/dinoderek/engine/internal/session"
)

// Tasks are a plan's tasks, or a standalone task alone, by number: the
// read-only projection that lint and coordination share. It only reads the
// snapshot; it never observes hosts or persists state.
func Tasks(st control.State, id string) []control.Item {
	var items []control.Item
	for _, t := range st.Items {
		if t.Plan == id || t.ID == id && t.Kind == "task" {
			items = append(items, t)
		}
	}
	slices.SortFunc(items, func(a, b control.Item) int { return control.Number(a.ID) - control.Number(b.ID) })
	return items
}

// view is what one pass decides on for root id: its records, and the
// fleet's observation of their sessions.
func (c *Coordinator) view(st control.State, id string, fleet reconcile.Fleet) reconcile.View {
	root := st.Items[id]
	v := reconcile.View{Now: c.now(), Fleet: fleet, Root: withSession(st, root, fleet)}
	tasks := Tasks(st, id)
	for _, t := range tasks {
		v.Tasks = append(v.Tasks, withSession(st, t, fleet))
	}
	if root.Kind == "plan" && len(tasks) > 0 {
		for _, p := range lint.Items(tasks) {
			v.Lint = append(v.Lint, p.String())
		}
	}
	return v
}

// record is item as a pass reads it from st, without its session.
func record(st control.State, item control.Item) reconcile.Item {
	t := reconcile.Item{Item: item, Current: st.Attempts[item.Attempt], Name: sessionName(st.Items[item.Repo].Slug, control.Number(item.ID)),
		Vanished: control.Vanished(&st, item), Question: control.PendingQuestion(&st, item.ID),
		Resume: hadEarlierAttempt(st, item.ID, ""), MergedAt: control.MergedAt(&st, item.ID)}
	for _, id := range item.Depends {
		if st.Items[id].State != "done" {
			t.Blocked = append(t.Blocked, id)
		}
	}
	if req, ok := control.PendingMerge(&st, item.ID); ok {
		t.Merge = &req
	}
	return t
}

// withSession is item's record with its current attempt's session, as the
// fleet shows it and as the attempt's marks say it was flagged. A vanished
// attempt has no session to observe.
func withSession(st control.State, item control.Item, fleet reconcile.Fleet) reconcile.Item {
	t := record(st, item)
	at, ok := st.Attempts[item.Attempt]
	if !ok || t.Vanished {
		return t
	}
	sess := observe(at, fleet, st.Items[item.Repo].Slug, control.Number(item.ID))
	sess.WaitingSince, sess.IdleSince = at.WaitingSince, at.IdleSince
	sess.StallNoted = at.Notice
	if sess.ResetsAt == 0 && sess.Waiting {
		sess.ResetsAt = at.ResetsAt
	}
	if at.ResumedFor == sess.ResetsAt {
		sess.ResumeAt = at.ResumeAt
	}
	sess.FarNoted = at.FarFor == sess.ResetsAt && sess.ResetsAt > 0
	sess.StuckNoted = at.StuckFor == sess.ResetsAt && sess.ResetsAt > 0
	t.Session = &sess
	return t
}

// observe is what the fleet shows of an attempt's session, on the host that
// runs it. A host that cannot be read, or whose run records cannot be, leaves
// it unknown; so does an unfinished dispatch without its window.
func observe(at control.Attempt, fleet reconcile.Fleet, slug string, number int) reconcile.Session {
	s := reconcile.Session{Name: sessionName(slug, number), Host: at.Host, WindowID: at.Window, Spawned: at.Started, Mode: "hands-off"}
	for _, h := range fleet.Hosts {
		if h.Name != at.Host {
			continue
		}
		if h.Unreachable != "" || h.Status.RunsError != "" {
			s.Unknown = true
			return s
		}
		if w, ok := matchWindow(at, h.Status, s.Name); ok {
			s.Live = w.Alive
			s.DeadWindow = !w.Alive
			s.Waiting = w.Alive && w.State == session.StateWaiting
			s.Background = w.Alive && w.State == session.StateWorking && w.Background
			s.Limited = w.Alive && w.State == session.StateLimited
			s.ResetsAt = w.ResetsAt
			s.Screen = w.Screen
			s.Denial = w.Denial
			s.Harness = at.Harness
			s.SignedOut = h.SignedOut(at.Harness)
			return s
		}
		s.Unknown = at.State == "dispatching"
		return s
	}
	s.Unknown = true
	return s
}

func (c *Coordinator) keepObservations(v reconcile.View) error {
	return c.Service.Store.Transaction(func(fresh *control.State) error {
		for _, t := range v.Sessions() {
			at := fresh.Attempts[t.Attempt]
			if at.ID == "" || t.Session.Unknown {
				continue
			}
			at = kept(at, *t.Session, v.Now, coordinating(fresh, fresh.Items[t.ID]))
			fresh.Attempts[at.ID] = at
		}
		return nil
	})
}

// coordinating reports whether item's coordination, its own or its plan's,
// still runs.
func coordinating(st *control.State, item control.Item) bool {
	return item.Coordinating || item.Plan != "" && st.Items[item.Plan].Coordinating
}

// since is the start of a spell that holds now: the recorded start, or now
// for a spell just begun. A spell that does not hold has none.
func since(start time.Time, holds bool, now time.Time) time.Time {
	switch {
	case !holds:
		return time.Time{}
	case start.IsZero():
		return now
	}
	return start
}

// kept is at with one pass's observation of its session applied: the starts
// of a wait and of an idle spell, a limit reset, and when it was seen live. A window
// is recorded as seen live only while coordination runs, so a pass that
// overlaps a stop never dates the sighting after it.
func kept(at control.Attempt, sess reconcile.Session, now time.Time, coordinating bool) control.Attempt {
	at.WaitingSince = since(at.WaitingSince, sess.Waiting && sess.Idle() && sess.ResetsAt == 0, now)
	at.IdleSince = since(at.IdleSince, sess.Idle() && sess.ResetsAt == 0, now)
	if sess.ResetsAt > 0 {
		at.ResetsAt = sess.ResetsAt
	}
	if sess.Live && coordinating {
		at.LiveAt = now
	}
	if sess.Live && !sess.Waiting && !sess.Limited {
		at.ResetsAt = 0
	}
	return at
}
