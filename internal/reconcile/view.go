// Package reconcile is the server coordinator's decision core. Decide reads
// one View, a coordinated plan with its server records
// and what the hosts show of its sessions, and returns the pass's actions.
// It is pure: it reads only the View, changes nothing, and knows no CLI.
package reconcile

import (
	"slices"
	"time"

	"github.com/dinoderek/engine/internal/control"
)

// View is everything one pass decides on.
type View struct {
	Now time.Time
	// Root is the coordinated plan.
	Root Item
	// Tasks are the plan's tasks by number.
	Tasks []Item
	// Lint is the plan's lint problems, for a plan with tasks.
	Lint  []string
	Fleet Fleet
}

// Item is one plan or task, with the records and observation a pass
// decides on.
type Item struct {
	control.Item
	// Current is the item's current attempt; its ID is empty when it has none.
	Current control.Attempt
	// Name is the item's session name, its tmux window's on any host.
	Name string
	// Session is the current attempt's session as the hosts show it, nil
	// when the item has no attempt or its attempt vanished.
	Session *Session
	// Vanished is true when the current attempt ended vanished, in an early
	// death or by a move: the task is dispatched again.
	Vanished bool
	// Question is the item's own unresolved operator question, or 0.
	Question int
	// PlanHold is the open plan question that keeps the task from a new
	// start, or 0. A task with an earlier attempt is never held by it.
	PlanHold int
	// RepoRequires are the capabilities the item's repository requires of
	// every task's host, on top of the card's own.
	RepoRequires []string
	// Blocked are the dependencies that are not done.
	Blocked []string
	// Resume is true when an earlier attempt opened a window, so the next
	// dispatch resumes the item's own branch.
	Resume bool
	// Stopped is true when the operator stopped the current attempt: its
	// window, if any is left, is released keeping the worktree and branch.
	Stopped bool
	// Claimed is true when the current attempt's session claimed its task
	// (control.Claimed).
	Claimed bool
	// Merge is the request of the task's merge queue entry, nil when it has
	// none.
	Merge *control.Update
	// MergedAt is when the coordinator recorded the item's merge, or zero.
	MergedAt time.Time
	// MergedHead is the head commit the PR merged at, or "" when unknown.
	MergedHead string
	// Cleaned is the task's newest cleanup report, nil when it has none.
	Cleaned *control.Update
	// Strays are the windows the task's attempts that ended moved still
	// have on hosts that answered, oldest first: each is released, pushing
	// its branch first, and the task waits for them. StrayUnknown is set
	// when such an attempt, not yet released, is on a host that could not be
	// read, so its window may still be there.
	Strays       []Stray
	StrayUnknown bool
}

// Stray is a moved attempt's window, as its host shows it.
type Stray struct {
	Attempt control.Attempt
	Session Session
}

// Kind is what an action does.
type Kind string

// The actions a pass can take.
const (
	// Dispatch reserves an attempt and launches a planner or executor.
	Dispatch Kind = "dispatch"
	// SetState moves a plan to planned or done; control.PlanStatus derives
	// the rest.
	SetState Kind = "state"
	// EndAttempt ends an attempt whose window died: the server decides
	// vanished, early-death or failed (control.Service.EndDead).
	EndAttempt Kind = "end"
	// Notice tells the operator once per key.
	Notice Kind = "notice"
	// Resume types continue after a usage limit resets.
	Resume Kind = "resume"
	// Release closes a finished session's window and worktree on its host.
	Release Kind = "release"
	// Merge calls the merge op on the host of a merge queue entry's task:
	// a merge for the head, a check for a waiting entry.
	Merge Kind = "merge"
	// QueueStep changes a merge queue entry.
	QueueStep Kind = "queue"
	// Stop clears the root's coordination intent.
	Stop Kind = "stop"
)

// Action is one thing a pass does. Only the fields of its Kind are set.
type Action struct {
	Kind Kind
	// Item is the plan or task acted on; for Stop, the root.
	Item string
	// Attempt is the attempt an EndAttempt, Resume, Release or Merge acts
	// on, or the one a Notice marks.
	Attempt string
	// Text says why: the notice's body, the ended attempt's reason, or the
	// log line.
	Text string
	// Tail is the pane log's end an EndAttempt records.
	Tail string
	// AtLimit is an EndAttempt of a window that died at a usage limit,
	// resetting at ResetsAt; Probe is set when the attempt tried a host ×
	// harness already at a limit, so its end tells no one.
	AtLimit bool
	Probe   bool
	// Host, Harness and Resume are where and how a Dispatch launches.
	Host    string
	Harness string
	Resume  bool
	// State is the plan state a SetState sets. Key, when set, is a notice
	// with Text sent once the state is set.
	State string
	// Key dedupes a Notice, or the notice a Release sends: one notice per
	// item and key.
	Key string
	// Stalled is the session start, the wait, or the last activity of a
	// quiet session, which a stall or quiet Notice flags.
	Stalled time.Time
	// Limit is the reset a limit Notice marks as alerted, far or stuck.
	Limit int64
	Far   bool
	// ResetsAt is the reset a Resume continues after, or an AtLimit
	// EndAttempt's.
	ResetsAt int64
	// Session is the window a Resume types into or a Release closes.
	Session Session
	// Done is a Release of a done task's session: the host runs the repo's
	// own worktree release too, and deletes the branch once it is merged.
	Done bool
	// Stopped is a Release of a stopped task's session: the host keeps its
	// worktree and branch. Moved is a Release of a moved attempt's: the
	// host pushes its branch first, and keeps both, unless Stopped is set
	// too.
	Stopped bool
	Moved   bool
	// Head is the head commit a done task's PR merged at.
	Head string
	// Entry is the merge queue entry a Merge acts on, Request its newest
	// request.
	Entry   control.QueueEntry
	Request control.Update
	// Step is the change a QueueStep makes.
	Step *control.QueueStep
}

// open reports whether the item is unfinished.
func (t Item) open() bool { return t.State != "done" }

// state is the item's state as coordination reads it: a vanished attempt
// leaves its task ready to dispatch again, unless an operator question
// parks it.
func (t Item) state() string {
	if t.Vanished && t.Question == 0 {
		return "todo"
	}
	return t.State
}

// claimStates are the task states an executor holds the task in.
var claimStates = []string{"running", "needs-human", "in-review"}

// claimed reports whether an executor holds the task.
func (t Item) claimed() bool { return slices.Contains(claimStates, t.state()) }

// taken reports whether the task may not start: it is held, or failed.
func (t Item) taken() bool { return t.claimed() || t.state() == "failed" }

// moveHeld reports whether a task the operator moved waits before its next
// dispatch: for a window a moved attempt left, which is released first, or
// for the old host to answer, unless the operator moved it while that host
// could not be read, or the host is gone from hosts.toml.
func (v View) moveHeld(t Item) bool {
	if len(t.Strays) > 0 {
		return true
	}
	at := t.Current
	if at.Reason != control.EndMoved || at.MovedBlind || at.MoveReleased {
		return false
	}
	// A host gone from hosts.toml has nothing left to release.
	h, ok := v.Fleet.Host(at.Host)
	return ok && (h.Unreachable != "" || h.Status.RunsError != "")
}

// AtHardStop reports whether the item waits on the operator: it is in
// needs-human, or has its own unresolved question. A session's wait there
// is the protocol's, so it never counts toward a stall.
func (t Item) AtHardStop() bool { return t.State == "needs-human" || t.Question > 0 }

// Wakeable reports whether a pointer at new updates may be typed into the
// item's session: the session is idle, so typing cuts into no turn, and the
// item is at no hard stop.
func (t Item) Wakeable() bool {
	return t.Session != nil && t.Session.Idle() && !t.AtHardStop() && !t.Stopped
}

// observed returns the item's session, or a zero one that is neither live
// nor unknown.
func (t Item) observed() Session {
	if t.Session == nil {
		return Session{}
	}
	return *t.Session
}

// Sessions are the items with an observed session, the root first, each
// once.
func (v View) Sessions() []Item {
	var out []Item
	for _, t := range append([]Item{v.Root}, v.Tasks...) {
		if t.Session != nil && !slices.ContainsFunc(out, func(o Item) bool { return o.ID == t.ID }) {
			out = append(out, t)
		}
	}
	return out
}
