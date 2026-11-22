package reconcile

import (
	"fmt"
	"strings"
	"time"
)

// quiets flags each task session (planners are flagged in planStep) that is working in a turn but has shown no
// activity for QuietWait, once per attempt and last activity time. A session
// that is idle, limited, held at a dialog, only in a background job or of
// unknown activity is never flagged.
func quiets(v View) []Action {
	var acts []Action
	for _, t := range v.Tasks {
		if t.open() && !t.Stopped {
			acts = append(acts, quiet(v, t)...)
		}
	}
	return acts
}

// quiet is t's notice that its working session has gone quiet, or nothing.
// The attempt keeps the last activity it was flagged for, as it keeps a
// stall's start, so one stretch of quiet is decided once however its text
// ages, and the next one, after new activity, is told again. The key holds
// the attempt and the last activity too.
func quiet(v View, t Item) []Action {
	s := t.Session
	if s == nil || !s.Quiet(v.Now, QuietWait) || s.Flagged(s.LastActivity) {
		return nil
	}
	a := notice(t, fmt.Sprintf("quiet/%s/%d", t.Current.ID, s.LastActivity.Unix()), quietText(t, *s, v.Now))
	a.Attempt, a.Stalled = t.Current.ID, s.LastActivity
	return []Action{a}
}

// quietText is the notice of a hung session: who, where, for how long, its
// screen, and the ways out. Nothing is stopped. A planner's ways out are the
// plan's.
func quietText(t Item, s Session, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s (%s) on %s is working but has shown no activity for %s, since %s: it may be hung.",
		s.Name, t.ID, s.Host, now.Sub(s.LastActivity).Truncate(time.Minute), s.LastActivity.UTC().Format(time.RFC3339))
	if screen := strings.TrimRight(s.Screen, "\n"); screen != "" {
		fmt.Fprintf(&b, "\n\nIts last screen lines:\n%s", screen)
	}
	ways := "engine task stop %[1]s, or engine task move %[1]s"
	if t.Kind == "plan" {
		ways = "engine plan stop %[1]s"
	}
	fmt.Fprintf(&b, "\n\nLook: engine attach %[1]s. Nudge it: engine tell %[1]s <text>. Or "+ways+". Nothing was stopped.", t.ID)
	return b.String()
}
