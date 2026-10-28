package run

import (
	"fmt"
	"strconv"
	"time"

	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/tracker"
)

const (
	resumeDelay = 2 * time.Minute
	resumeWait  = 10 * time.Minute
	farReset    = 24 * time.Hour
)

// Reset times name the notes; their contents are when the action happened.
// Keeping them across coordinator restarts avoids another continue for a reset.
func resetNote(prefix string, s Session) string {
	return prefix + "=" + strconv.FormatInt(s.ResetsAt, 10)
}

// A derived quota limit disappears from status when its quota window resets.
// Retain that observation for a waiting window until it is seen working.
// The start in the filename keeps a new session from inheriting an old limit.
func keepLimit(dir string, s *Session, known bool) error {
	if !known {
		return nil
	}
	note := "limit-reset-" + strconv.FormatInt(s.Spawned.Unix(), 10)
	if !s.Live || !s.Limited && !s.Waiting {
		return session.ClearNote(dir, note)
	}
	if s.ResetsAt > 0 {
		return session.WriteNote(dir, note, time.Unix(s.ResetsAt, 0))
	}
	reset, ok, err := session.ReadNote(dir, note)
	if err != nil {
		return err
	}
	if ok {
		s.ResetsAt = reset.Unix()
	}
	return nil
}

func readResumeNotes(dir string, s *Session) error {
	if s.ResetsAt <= 0 {
		return nil
	}
	for _, prefix := range []string{"resumed-for", "limit-far", "limit-stuck"} {
		at, ok, err := session.ReadNote(dir, resetNote(prefix, *s))
		if err != nil {
			return err
		}
		if !ok || at.Before(s.Spawned.Truncate(time.Second)) {
			continue
		}
		switch prefix {
		case "resumed-for":
			s.ResumeAt = at
		case "limit-far":
			s.FarNoted = true
		case "limit-stuck":
			s.StuckNoted = true
		}
	}
	return nil
}

func limitActions(v View, ref tracker.Ref) []Action {
	s := v.Sessions[ref]
	if !s.Live || s.Unknown || s.WindowID == "" || s.ResetsAt <= 0 || !s.Limited && !s.Waiting {
		return nil
	}
	reset := time.Unix(s.ResetsAt, 0)
	a := Action{Ref: ref, Session: s}
	switch {
	case reset.Sub(v.Now) > farReset:
		if s.FarNoted {
			return nil
		}
		a.Kind, a.Text = LimitAlert, fmt.Sprintf("%s has a usage limit resetting more than 24 hours away (%s); waiting without typing", s.Name, reset.UTC().Format(time.RFC3339))
	case s.ResumeAt.IsZero() && !v.Now.Before(reset.Add(resumeDelay)):
		a.Kind, a.Text = Resume, "continue; "+resetNote("resumed-for", s)
	case s.Limited && !s.ResumeAt.IsZero() && v.Now.Sub(s.ResumeAt) >= resumeWait && !s.StuckNoted:
		a.Kind, a.Text = LimitAlert, s.Name+" is still limited 10 minutes after continue; "+resetNote("resumed-for", s)
	default:
		return nil
	}
	return []Action{a}
}

func resume(w World, v View, a Action) error {
	dir, err := w.NoteDir(a.Ref)
	if err != nil {
		return err
	}
	// Record the attempt before typing: an Enter failure must not paste the
	// text twice on the next check. A session still limited gets an alert.
	if err := session.WriteNote(dir, resetNote("resumed-for", a.Session), v.Now); err != nil {
		return err
	}
	return w.Resume(a.Session)
}

func limitAlert(w World, v View, a Action) error {
	// Use the stall's screen and attach instructions, with separate notes so
	// an ordinary wait never suppresses a limit alert or vice versa.
	dir, err := w.NoteDir(a.Ref)
	if err != nil {
		return err
	}
	if err := w.Comment(a.Ref, sessionReport(w, v, a)); err != nil {
		return err
	}
	prefix := "limit-stuck"
	if time.Unix(a.Session.ResetsAt, 0).Sub(v.Now) > farReset {
		prefix = "limit-far"
	}
	return session.WriteNote(dir, resetNote(prefix, a.Session), v.Now)
}
