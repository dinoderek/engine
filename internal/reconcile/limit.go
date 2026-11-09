package reconcile

import (
	"fmt"
	"strconv"
	"time"
)

// The limit timings.
const (
	// ResumeDelay is how long after its reset a limited session is told to
	// continue.
	ResumeDelay = 2 * time.Minute
	// resumeWait is how long a session may stay limited after continue
	// before the operator is told.
	resumeWait = 10 * time.Minute
	// farReset is how far off a reset is alerted instead of awaited.
	farReset = 24 * time.Hour
)

// LimitStep is what a session stopped on a usage limit calls for now.
type LimitStep int

// The limit steps.
const (
	// NoLimitStep: nothing to do.
	NoLimitStep LimitStep = iota
	// LimitResume: type continue, the reset having passed.
	LimitResume
	// LimitFar: alert once that the reset is more than a day away.
	LimitFar
	// LimitStuck: alert once that the session is still limited after continue.
	LimitStuck
)

// ResetNote names a mark kept for one reset, such as resumed-for=<resets_at>.
func ResetNote(prefix string, s Session) string {
	return prefix + "=" + strconv.FormatInt(s.ResetsAt, 10)
}

// Limit is the step, and its text, that a live session with a known usage
// limit reset calls for at now. The caller has checked the session is not
// at a hard stop.
func Limit(s Session, now time.Time) (LimitStep, string) {
	if !s.Live || s.Unknown || s.WindowID == "" || s.ResetsAt <= 0 || !s.Limited && !s.Waiting {
		return NoLimitStep, ""
	}
	reset := time.Unix(s.ResetsAt, 0)
	switch {
	case reset.Sub(now) > farReset:
		if s.FarNoted {
			return NoLimitStep, ""
		}
		return LimitFar, fmt.Sprintf("%s has a usage limit resetting more than 24 hours away (%s); waiting without typing", s.Name, reset.UTC().Format(time.RFC3339))
	case s.ResumeAt.IsZero() && !now.Before(reset.Add(ResumeDelay)):
		return LimitResume, "continue; " + ResetNote("resumed-for", s)
	case s.Limited && !s.ResumeAt.IsZero() && now.Sub(s.ResumeAt) >= resumeWait && !s.StuckNoted:
		return LimitStuck, s.Name + " is still limited 10 minutes after continue; " + ResetNote("resumed-for", s)
	}
	return NoLimitStep, ""
}
