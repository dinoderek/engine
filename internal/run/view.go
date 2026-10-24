// Package run is engine run: the coordinator's reconcile loop for one
// feature, or one task, on this host. Each pass reads the tracker and the
// host into a View, Decide works out what to do, and Apply does it. The pass
// follows the engine skill's coordinate reference
// (plugins/engine/skills/engine/references/coordinate.md): the skill is the
// spec, and this package follows it.
package run

import (
	"slices"
	"time"

	"github.com/dinoderek/engine/internal/ps"
	"github.com/dinoderek/engine/internal/tracker"
)

// The waits the pass acts on.
const (
	// ClaimWait is how long a started session has to claim its task.
	ClaimWait = 5 * time.Minute
	// StallWait is how long a session may wait for input outside a hard stop.
	StallWait = 10 * time.Minute
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

// Session is the newest run engine started on this host for a task, or for
// a feature's planner.
type Session struct {
	Name     string
	WindowID string
	RunDir   string
	Mode     string
	Spawned  time.Time
	// Live is true while its window is open and its command runs.
	Live bool
	// Waiting is true when Claude Code says it waits for input.
	Waiting      bool
	WaitingSince time.Time
	StallNoted   time.Time
}

// waited reports whether the session has waited for input for d by now.
func (s Session) waited(now time.Time, d time.Duration) bool {
	return s.Live && s.Waiting && !s.WaitingSince.IsZero() && now.Sub(s.WaitingSince) >= d
}

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
	FreeSlots int
	Now       time.Time
}

// Kind is what an action does.
type Kind string

// The actions a pass can take.
const (
	StartTask    Kind = "start"
	StartPlanner Kind = "plan"
	SetLabel     Kind = "label"
	ReportLint   Kind = "lint"
	Stall        Kind = "stall"
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
	// Text says why, for the log and for the comment the action posts.
	Text    string
	Session Session
}

func hasAny(labels []string, set []string) bool {
	return slices.ContainsFunc(labels, func(l string) bool { return slices.Contains(set, l) })
}
