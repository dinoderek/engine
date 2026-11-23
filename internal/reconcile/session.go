package reconcile

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hostops"
)

// The waits a pass acts on.
const (
	// ClaimWait is how long a started session has to claim its task.
	ClaimWait = control.ClaimWait
	// StallWait is how long a session may wait for input outside a hard stop.
	StallWait = 10 * time.Minute
	// DenialLead is how long before a wait was first seen a permission
	// denial may have been logged and still be the wait's cause: a session
	// stops soon after a denial it has no route around, and the coordinator
	// sees the wait on a later pass.
	DenialLead = 10 * time.Minute
	// CleanupWait is how long after its merge an idle session that has not
	// reported its cleanup keeps its window before it is released.
	CleanupWait = 15 * time.Minute
	// CleanupCap is how long after its merge a session still in a turn,
	// with no cleanup reported, runs before the operator is told.
	CleanupCap = 30 * time.Minute
	// QuietWait is how long a working session may show no activity before
	// it is reported as hung.
	QuietWait = hostops.QuietWait
)

// Session is the newest run of a task, or of a plan's planner, on any host.
type Session struct {
	Name string
	// Host is where it runs. When that host could not be read, Unknown is
	// true and nothing else is known: the session is neither live nor
	// dead, and nothing is done to it.
	Host     string
	Unknown  bool
	WindowID string
	Mode     string
	Spawned  time.Time
	// Live is true while its window is open and its command runs.
	Live bool
	// DeadWindow is true when its window, by id and name, is still open with
	// its command exited. Only then is the window its own to close: tmux
	// reuses ids after a restart.
	DeadWindow bool
	// Limited is true while a live window is stopped on a usage limit;
	// ResetsAt is its reset in epoch seconds, or zero if unknown.
	Limited  bool
	ResetsAt int64
	// ResumeAt and the alert marks belong to this reset and session start.
	// LimitedSince is when it was first seen limited in its current spell.
	ResumeAt     time.Time
	LimitedSince time.Time
	FarNoted     bool
	StuckNoted   bool
	// LimitAsked is set when a fleet question about its usage limit covers
	// it (LimitChoices): the question stands in for the far-reset notice.
	LimitAsked bool
	// Working is true when its window's state, from status, is working in a
	// turn: not limited, waiting or only in a background job. LastActivity
	// is when its window last showed activity (hostops.Window), zero when
	// the host does not know.
	Working      bool
	LastActivity time.Time
	// Waiting is true when its window's state, from status, is waiting.
	Waiting bool
	// Dialog is true when its window's state is waiting at a dialog that
	// typed input would answer, such as a trust prompt whose default exits.
	// Waiting is then false: the session stalls as a wait does, but is not
	// idle, so nothing is typed into it.
	Dialog bool
	// Background is true when its window's state is working but its last
	// turn has ended: only a background job keeps it busy, and it takes
	// typed input as a waiting session does.
	Background bool
	// WaitingSince is when it was first seen waiting in its current wait,
	// and IdleSince when it was first seen idle in its current spell:
	// waiting, or working only in a background job.
	WaitingSince time.Time
	IdleSince    time.Time
	// StallNoted is the start, the wait or the last activity the coordinator
	// last flagged.
	StallNoted time.Time
	// Screen is the window's last lines, when its host sent them: it does
	// for a window whose session is not working, or is working but quiet.
	Screen string
	// PaneTail is the end of the session's pane log, which its host shows
	// for a run whose window is gone or dead.
	PaneTail string
	// DiedAtLimit is true when its window is gone or dead and its run's
	// limit marker is set: it died at a usage limit, whose reset is then
	// ResetsAt.
	DiedAtLimit bool
	// Denial is the newest entry of a waiting session's permission log,
	// when that entry is a denial.
	Denial *hostops.Denial
	// Harness is what the session runs, and SignedOut is true when its host
	// says that harness is signed out there.
	Harness   string
	SignedOut bool
	// Worktree and Branch are the attempt's run's, when its host lists that
	// run.
	Worktree string
	Branch   string
}

// Flagged reports whether at, the session's start, the start of its wait or
// its last activity, is the one the coordinator last flagged. Marks keep whole seconds.
// Equality, not order, so hosts' clocks need not agree.
func (s Session) Flagged(at time.Time) bool {
	return s.StallNoted.Truncate(time.Second).Equal(at.Truncate(time.Second))
}

// Waited reports whether the session has waited for input for d by now.
func (s Session) Waited(now time.Time, d time.Duration) bool {
	return (s.Waiting && s.Idle() || s.AtDialog()) && s.ResetsAt <= 0 && !s.WaitingSince.IsZero() && now.Sub(s.WaitingSince) >= d
}

// AtDialog reports whether the live session is held at a dialog.
func (s Session) AtDialog() bool {
	return s.Live && !s.Unknown && !s.Limited && s.Dialog
}

// Quiet reports whether the session is working in a turn and has shown no
// activity for d by now. Unknown activity is never quiet.
func (s Session) Quiet(now time.Time, d time.Duration) bool {
	return s.Live && !s.Unknown && !s.Limited && s.Working && !s.LastActivity.IsZero() && now.Sub(s.LastActivity) >= d
}

// Idle reports whether the session takes typed input without cutting into
// a turn: it waits for input, or only a background job keeps it busy. A
// limited session waits on its reset instead.
func (s Session) Idle() bool {
	return s.Live && !s.Unknown && !s.Limited && (s.Waiting || s.Background)
}

// IdleFor reports whether the session has been idle, with no limit reset
// pending, for d by now.
func (s Session) IdleFor(now time.Time, d time.Duration) bool {
	return s.Idle() && s.ResetsAt <= 0 && !s.IdleSince.IsZero() && now.Sub(s.IdleSince) >= d
}

// Denied reports whether the session's wait follows a permission denial:
// its newest permission log entry is a denial logged no more than
// DenialLead before the wait was first seen.
func (s Session) Denied() bool {
	return s.Denial != nil && !s.WaitingSince.IsZero() && !s.Denial.At.Before(s.WaitingSince.Add(-DenialLead))
}

// WaitText is a waiting session's stall text: plain, unless its host says
// its harness is signed out, which names the sign-in, or the wait follows a
// permission denial. Then it is the Permission hard stop the session did
// not raise, naming the tool, its exact input and why. A signed-out wait
// leads with the sign-in and names the denial too. A session held at a
// dialog says so instead.
func WaitText(s Session, plain string) string {
	if s.AtDialog() {
		return DialogText(strings.TrimSuffix(plain, ", outside a hard stop"))
	}
	denial := ""
	if s.Denied() {
		d := s.Denial
		why := d.Decision
		if d.Reason != "" {
			why += ": " + d.Reason
		}
		denial = fmt.Sprintf("%s %s was %s", d.Tool, deniedInput(d.Input), why)
	}
	if s.SignedOut {
		text := fmt.Sprintf("%s, while %s. Sign it in there, then type continue in the window",
			strings.TrimSuffix(plain, ", outside a hard stop"), signedOutText(s.Host, s.Harness))
		if denial != "" {
			text += ". It also follows a denied permission request, the Permission hard stop (hard-stops.md): " + denial
		}
		return text
	}
	if denial == "" {
		return plain
	}
	return fmt.Sprintf("%s has waited for input for %s after a denied permission request, the Permission hard stop (hard-stops.md): "+
		"%s. Allow it with a permission rule in the session, or do it by hand, then tell the session to carry on",
		s.Name, StallWait, denial)
}

// DialogText is a stall text for a session held at a dialog: the operator
// answers it in the window, since typing there would take its default. A
// harness may infer the dialog, so the text says likely.
func DialogText(plain string) string {
	return plain + ": its harness says it is likely held at a dialog in its window. Answer it there; engine types nothing into it"
}

// deniedInput is a denied tool's input quoted for a comment: a shell
// command as itself, any other input as its JSON, cut to maxInput
// characters, with backticks made quotes. The exact input stays in the
// run's permissions.jsonl.
func deniedInput(raw json.RawMessage) string {
	var bash struct {
		Command string `json:"command"`
	}
	text := strings.TrimSpace(string(raw))
	if json.Unmarshal(raw, &bash) == nil && bash.Command != "" {
		text = bash.Command
	}
	if r := []rune(text); len(r) > maxInput {
		text = string(r[:maxInput]) + "…"
	}
	return "`" + strings.ReplaceAll(text, "`", "'") + "`"
}

// maxInput is how much of a denied tool's input a stall comment quotes.
const maxInput = 500
