// Package run is engine run: the coordinator's reconcile loop for one
// feature, or one task, on this host. Each pass reads the tracker and the
// host into a View, Decide works out what to do, and Apply does it. The pass
// is specified by this package's code alone.
package run

import (
	"slices"
	"time"

	"github.com/dinoderek/engine/internal/ps"
	"github.com/dinoderek/engine/internal/reconcile"
	"github.com/dinoderek/engine/internal/tracker"
)

// The waits the pass acts on.
const (
	// ClaimWait is how long a started session has to claim its task.
	ClaimWait = reconcile.ClaimWait
	// StallWait is how long a session may wait for input outside a hard stop.
	StallWait = reconcile.StallWait
	// DoneWait is how long the run, once the work is done, waits for the
	// hands-off windows of closed tasks to go quiet before it exits.
	DoneWait = 15 * time.Minute
	// StartRetry is how long a task whose start failed waits to be tried
	// again; until then it holds no slot.
	StartRetry = 30 * time.Minute
	// DenialLead is how long before a wait was first seen a permission
	// denial may have been logged and still be the wait's cause.
	DenialLead = reconcile.DenialLead
)

// Labels the pass reads and writes.
const (
	Approved   = "engine:approved"
	Planned    = "engine:planned"
	Executing  = "engine:executing"
	Verifying  = "engine:verifying"
	Blocked    = "engine:blocked"
	Done       = "engine:done"
	Running    = "engine:running"
	NeedsHuman = "engine:needs-human"
	InReview   = "engine:in-review"
	Failed     = "engine:failed"
	Merging    = "engine:merging"
	Rulings    = "engine:ruling-review"
)

// featureStates are the feature labels of which a feature carries one.
var featureStates = []string{"engine:prepare", Approved, Planned, Executing, Verifying, Blocked, Done}

// claimStates are the task labels an executor sets while it holds the task.
var claimStates = []string{Running, NeedsHuman, InReview}

// takenStates are the task labels that keep a task from starting.
var takenStates = append(slices.Clone(claimStates), Failed)

// Session is the newest run of a task, or of a feature's planner, on any
// host: the reconcile core's.
type Session = reconcile.Session

// View is everything one pass decides on.
type View struct {
	Feature tracker.Feature
	// TaskOnly is true when engine run drives one task: Feature then holds
	// that task alone, and the feature's own labels are left alone.
	TaskOnly bool
	// Rows are the claimed tasks of the feature, as engine ps settles them.
	Rows map[tracker.Ref]ps.Row
	// Sessions are keyed by task, and by the feature for its planner.
	Sessions map[tracker.Ref]Session
	// LintProblems are the plan's problems, read once the planner is done.
	LintProblems []string
	// LintNoted is true once the coordinator has reported them.
	LintNoted bool
	// StartFailed is when each task's last start failed, if it did.
	StartFailed map[tracker.Ref]time.Time
	// PlaceNoted is true for each task the coordinator has said no host
	// can take.
	PlaceNoted map[tracker.Ref]bool
	// Operator is the issue whose assignee, else author, is @mentioned:
	// the feature, or a lone task's feature.
	Operator tracker.Ref
	Fleet    Fleet
	Now      time.Time
}

// Kind is what an action does.
type Kind string

// The actions a pass can take.
const (
	StartTask    Kind = "start"
	Wait         Kind = "wait"
	StartPlanner Kind = "plan"
	SetLabel     Kind = "label"
	ReportLint   Kind = "lint"
	Stall        Kind = "stall"
	Resume       Kind = "resume"
	LimitAlert   Kind = "limit-alert"
	Fail         Kind = "fail"
	Reap         Kind = "reap"
	Finish       Kind = "finish"
	Exit         Kind = "exit"
)

// Action is one thing a pass does.
type Action struct {
	Kind Kind
	Ref  tracker.Ref
	// Label is the feature state SetLabel sets.
	Label string
	// Host is where StartTask starts the task.
	Host    string
	Harness string
	// Text says why, for the log and for the comment the action posts.
	Text    string
	Session Session
	// Failure marks an Exit that ends the run short of done.
	Failure bool
	// Notify asks a Wait to comment its reason on the task.
	Notify bool
	// At is the session start, or the wait, a Stall flags.
	At time.Time
}

func hasAny(labels []string, set []string) bool {
	return slices.ContainsFunc(labels, func(l string) bool { return slices.Contains(set, l) })
}
