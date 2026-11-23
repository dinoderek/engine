package controlrun

import (
	"slices"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/lint"
	"github.com/dinoderek/engine/internal/reconcile"
	"github.com/dinoderek/engine/internal/session"
)

// Tasks are a plan's tasks by number: the read-only projection that lint and
// coordination share. It only reads; it never observes hosts or persists
// state.
func Tasks(tx *control.Tx, id string) []control.Item { return TasksOf(tx.ItemsOfPlan(id), id) }

// TasksOf are the tasks of plan id among items, by number.
func TasksOf(items []control.Item, id string) []control.Item {
	var tasks []control.Item
	for _, t := range items {
		if t.Kind == "task" && t.Plan == id {
			tasks = append(tasks, t)
		}
	}
	slices.SortFunc(tasks, func(a, b control.Item) int { return control.Number(a.ID) - control.Number(b.ID) })
	return tasks
}

// view is what one pass decides on for root id: its records, and the
// fleet's observation of their sessions.
func (c *Coordinator) view(tx *control.Tx, id string, fleet reconcile.Fleet) reconcile.View {
	root, _ := tx.Item(id)
	fleet.Now, fleet.Limits = c.now(), control.HarnessLimits(tx)
	fleet.Hosts = pausedHosts(tx, fleet.Hosts)
	v := reconcile.View{Now: fleet.Now, Fleet: fleet, Root: withSession(tx, root, fleet)}
	tasks := Tasks(tx, id)
	for _, t := range tasks {
		v.Tasks = append(v.Tasks, withSession(tx, t, fleet))
	}
	if len(tasks) > 0 {
		for _, p := range lint.Items(tasks) {
			v.Lint = append(v.Lint, p.String())
		}
	}
	return v
}

// pausedHosts is hosts with the operator's pauses marked; the pass's own list
// is left as it was.
func pausedHosts(tx *control.Tx, hosts []reconcile.HostLoad) []reconcile.HostLoad {
	out := slices.Clone(hosts)
	for i := range out {
		out[i].Paused = tx.Host(out[i].Name).Paused
	}
	return out
}

// record is item as a pass reads it, without its session.
func record(tx *control.Tx, item control.Item) reconcile.Item {
	current, _ := tx.Attempt(item.Attempt)
	repo, _ := tx.Item(item.Repo)
	t := reconcile.Item{Item: item, Current: current, Name: sessionName(repo.Slug, control.Number(item.ID)),
		Vanished: control.Vanished(tx, item), Question: control.OwnQuestion(tx, item.ID), PlanHold: control.NewStartHold(tx, item.ID),
		Stopped: current.Reason == control.EndStopped, Claimed: control.Claimed(tx, current), Resume: hadEarlierAttempt(tx, item.ID, ""), MergedAt: control.MergedAt(tx, item.ID), MergedHead: control.MergedHead(tx, item.ID)}
	for _, id := range item.Depends {
		if dep, _ := tx.Item(id); dep.State != "done" {
			t.Blocked = append(t.Blocked, id)
		}
	}
	if req, ok := control.PendingMerge(tx, item.ID); ok {
		t.Merge = &req
	}
	if u, ok := control.CleanedUp(tx, item.ID); ok {
		t.Cleaned = &u
	}
	return t
}

// withSession is item's record with its current attempt's session, as the
// fleet shows it and as the attempt's marks say it was flagged. A vanished
// attempt has no session to observe.
func withSession(tx *control.Tx, item control.Item, fleet reconcile.Fleet) reconcile.Item {
	t := record(tx, item)
	t.Strays, t.StrayUnknown = strays(tx, item, fleet)
	at, ok := tx.Attempt(item.Attempt)
	if !ok || t.Vanished {
		return t
	}
	repo, _ := tx.Item(item.Repo)
	sess := observe(at, fleet, repo.Slug, control.Number(item.ID))
	sess.WaitingSince, sess.IdleSince, sess.LimitedSince = at.WaitingSince, at.IdleSince, at.LimitedSince
	sess.StallNoted = at.Notice
	if sess.ResetsAt == 0 && sess.Waiting {
		sess.ResetsAt = at.ResetsAt
	}
	if at.ResumedFor == sess.ResetsAt {
		sess.ResumeAt = at.ResumeAt
	}
	sess.FarNoted = at.FarFor == sess.ResetsAt && sess.ResetsAt > 0
	sess.StuckNoted = at.StuckFor == sess.ResetsAt && sess.ResetsAt > 0
	sess.LimitAsked = slices.Contains(tx.Host(at.Host).Limits[at.Harness].Tasks, item.ID)
	t.Session = &sess
	return t
}

// strays are item's attempts that ended moved, and were not yet released,
// whose run is still their host's newest of its name on a host that
// answered, oldest first: their window, live, dead or already gone, is
// released, pushing the branch. Only such a run's window is the attempt's,
// as a later session there may reuse its tmux id. unknown is set when such
// an attempt's host could not be read; a host gone from hosts.toml has
// nothing left to release.
func strays(tx *control.Tx, item control.Item, fleet reconcile.Fleet) (out []reconcile.Stray, unknown bool) {
	repo, _ := tx.Item(item.Repo)
	for _, at := range tx.AttemptsFor(item.ID) {
		if _, configured := fleet.Host(at.Host); at.Reason != control.EndMoved || at.Window == "" || at.MoveReleased || !configured {
			continue
		}
		s := observe(at, fleet, repo.Slug, control.Number(item.ID))
		switch {
		case s.Unknown:
			unknown = true
		case newestRun(fleet, at, s.Name):
			out = append(out, reconcile.Stray{Attempt: at, Session: s})
		}
	}
	slices.SortFunc(out, func(a, b reconcile.Stray) int { return control.Number(a.Attempt.ID) - control.Number(b.Attempt.ID) })
	return out, unknown
}

// newestRun reports whether at's host shows at's run as its newest of name.
func newestRun(fleet reconcile.Fleet, at control.Attempt, name string) bool {
	h, ok := fleet.Host(at.Host)
	return ok && slices.ContainsFunc(h.Status.Runs, func(r hostops.Run) bool { return r.Name == name && r.Record.RunID == at.RunID })
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
		for _, r := range h.Status.Runs {
			if r.Name == s.Name && r.Record.RunID == at.RunID {
				s.Worktree, s.Branch, s.PaneTail = r.Record.Worktree, r.Record.Branch, r.PaneTail
				if r.Limit != nil {
					s.DiedAtLimit, s.ResetsAt = true, r.Limit.ResetsAt
				}
			}
		}
		if w, ok := matchWindow(at, h.Status, s.Name); ok {
			withWindow(&s, w)
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

// withWindow sets what the session's window says of it.
func withWindow(s *reconcile.Session, w hostops.Window) {
	s.Live = w.Alive
	s.DeadWindow = !w.Alive
	s.Dialog = w.Alive && w.State == session.StateWaiting && w.Dialog
	s.Waiting = w.Alive && w.State == session.StateWaiting && !w.Dialog
	s.Working = w.Alive && w.State == session.StateWorking && !w.Background
	s.Background = w.Alive && w.State == session.StateWorking && w.Background
	if w.Alive && w.LastActivity > 0 {
		s.LastActivity = time.Unix(w.LastActivity, 0)
	}
	s.Limited = w.Alive && w.State == session.StateLimited
	s.DiedAtLimit = s.DiedAtLimit && !w.Alive
	if w.Alive {
		s.ResetsAt = w.ResetsAt
	}
	s.Screen = w.Screen
	s.Denial = w.Denial
}

func (c *Coordinator) keepObservations(v reconcile.View) error {
	return write(c.Service.Store, func(tx *control.Tx) error {
		for _, t := range v.Sessions() {
			at, _ := tx.Attempt(t.Attempt)
			if at.ID == "" || t.Session.Unknown {
				continue
			}
			item, _ := tx.Item(t.ID)
			tx.PutAttempt(kept(at, *t.Session, v.Now, coordinating(tx, item)))
		}
		return nil
	})
}

// coordinating reports whether item's plan coordination still runs.
func coordinating(tx *control.Tx, item control.Item) bool {
	return control.Root(tx, item).Coordinating
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
	at.WaitingSince = since(at.WaitingSince, (sess.Waiting && sess.Idle() || sess.AtDialog()) && sess.ResetsAt == 0, now)
	at.IdleSince = since(at.IdleSince, sess.Idle() && sess.ResetsAt == 0, now)
	at.LimitedSince = since(at.LimitedSince, sess.Live && sess.Limited, now)
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
