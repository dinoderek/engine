package reconcile

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dinoderek/engine/internal/hostops"
)

// The waits a pass acts on.
const (
	// ClaimWait is how long a started session has to claim its task.
	ClaimWait = 5 * time.Minute
	// StallWait is how long a session may wait for input outside a hard stop.
	StallWait = 10 * time.Minute
	// DenialLead is how long before a wait was first seen a permission
	// denial may have been logged and still be the wait's cause: a session
	// stops soon after a denial it has no route around, and the coordinator
	// sees the wait on a later pass.
	DenialLead = 10 * time.Minute
	// ReportWindow is how long a merged task's session keeps its window,
	// while waiting, to report before it is released.
	ReportWindow = 10 * time.Minute
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
	ResumeAt   time.Time
	FarNoted   bool
	StuckNoted bool
	// Waiting is true when its window's state, from status, is waiting.
	Waiting      bool
	WaitingSince time.Time
	// StallNoted is the start, or the wait, the coordinator last flagged.
	StallNoted time.Time
	// Screen is the window's last lines, when its host sent them: it does
	// for a window whose session is not working.
	Screen string
	// Denial is the newest entry of a waiting session's permission log,
	// when that entry is a denial.
	Denial *hostops.Denial
	// Harness is what the session runs, and SignedOut is true when its host
	// says that harness is signed out there.
	Harness   string
	SignedOut bool
}

// Flagged reports whether at, the session's start or the start of its
// wait, is the one the coordinator last flagged. Marks keep whole seconds.
// Equality, not order, so hosts' clocks need not agree.
func (s Session) Flagged(at time.Time) bool {
	return s.StallNoted.Truncate(time.Second).Equal(at.Truncate(time.Second))
}

// Waited reports whether the session has waited for input for d by now.
func (s Session) Waited(now time.Time, d time.Duration) bool {
	return s.Live && !s.Limited && s.ResetsAt <= 0 && s.Waiting && !s.WaitingSince.IsZero() && now.Sub(s.WaitingSince) >= d
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
// leads with the sign-in and names the denial too.
func WaitText(s Session, plain string) string {
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
