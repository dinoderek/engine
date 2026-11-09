package run

import (
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/dinoderek/engine/internal/reconcile"
	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/tracker"
)

// farReset is how far off a reset makes a limit alert far.
const farReset = 24 * time.Hour

// Reset times name the notes; their contents are when the action happened.
// Keeping them across coordinator restarts avoids another continue for a reset.
var resetNote = reconcile.ResetNote

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
	if atHardStop(v, ref) {
		return nil
	}
	s := v.Sessions[ref]
	kind := map[reconcile.LimitStep]Kind{reconcile.LimitResume: Resume, reconcile.LimitFar: LimitAlert, reconcile.LimitStuck: LimitAlert}
	step, text := reconcile.Limit(s, v.Now)
	if step == reconcile.NoLimitStep {
		return nil
	}
	return []Action{{Kind: kind[step], Ref: ref, Session: s, Text: text}}
}

func atHardStop(v View, ref tracker.Ref) bool {
	return ref == v.Feature.Ref && v.Feature.Has(NeedsHuman) || slices.ContainsFunc(v.Feature.Tasks, func(c tracker.Card) bool {
		return c.Ref == ref && c.Has(NeedsHuman)
	})
}

func resume(w World, v View, a Action) error {
	card, _, err := w.Task(a.Ref)
	if err != nil {
		return fmt.Errorf("recheck task before automatic resume: %w", err)
	}
	if card.Has(NeedsHuman) {
		w.Logf("skip automatic resume of %s: %s", a.Ref, NeedsHuman)
		return nil
	}
	w.Logf("%s %s: %s", a.Kind, a.Ref, logText(a))
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
