// Package reconcile is the server coordinator's decision core. Decide reads
// one View, a coordinated plan or standalone task with its server records
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
	// Root is the coordinated plan, or standalone task.
	Root Item
	// Tasks are a plan's tasks by number, or a standalone task alone.
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
	// Vanished is true when the current attempt ended vanished: the task
	// stays resumable.
	Vanished bool
	// Question is the item's, or its plan's, unresolved operator question,
	// or 0.
	Question int
	// Blocked are the dependencies that are not done.
	Blocked []string
	// Resume is true when an earlier attempt opened a window, so the next
	// dispatch resumes the item's own branch.
	Resume bool
	// Merge is the pending merge request, nil when there is none.
	Merge *control.Update
	// MergeHeld is true while a failed merge waits for the operator to
	// acknowledge it.
	MergeHeld bool
	// Locked is true when the current attempt holds the repository's merge
	// lock.
	Locked bool
	// MergedAt is when the coordinator recorded the item's merge, or zero.
	MergedAt time.Time
}

// Kind is what an action does.
type Kind string

// The actions a pass can take.
const (
	// Dispatch reserves an attempt and launches a planner or executor.
	Dispatch Kind = "dispatch"
	// SetState moves a plan through planned, executing, verifying, blocked
	// and done.
	SetState Kind = "state"
	// EndAttempt ends an attempt whose window died: failed, or vanished when
	// the server finds the task resumable.
	EndAttempt Kind = "end"
	// Notice tells the operator once per key.
	Notice Kind = "notice"
	// Resume types continue after a usage limit resets.
	Resume Kind = "resume"
	// Release closes a finished session's window and worktree on its host.
	Release Kind = "release"
	// Merge merges a requested PR on the task's host, or refuses it.
	Merge Kind = "merge"
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
	// Host, Harness and Resume are where and how a Dispatch launches.
	Host    string
	Harness string
	Resume  bool
	// State is the plan state a SetState sets. Key, when set, is a notice
	// with Text sent once the state is set.
	State string
	// Key dedupes a Notice: one notice per item and key.
	Key string
	// Stalled is the session start, or the wait, a stall Notice flags.
	Stalled time.Time
	// Limit is the reset a limit Notice marks as alerted, far or stuck.
	Limit int64
	Far   bool
	// ResetsAt is the reset a Resume continues after.
	ResetsAt int64
	// Session is the window a Resume types into or a Release closes.
	Session Session
	// Request is the merge request a Merge answers. Refuse, when set, is the
	// merge-refused reason; otherwise the PR is merged.
	Request control.Update
	Refuse  string
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
