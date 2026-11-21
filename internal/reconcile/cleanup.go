package reconcile

import (
	"cmp"
	"fmt"
	"strings"
)

// reaps releases each done task's session: a merged task's by the cleanup
// rule (afterMerge), any other once it has been idle StallWait.
func reaps(v View) []Action {
	var acts []Action
	for _, t := range v.Tasks {
		s := t.Session
		if s == nil || t.open() || s.Unknown || t.Current.State == "ended" {
			continue
		}
		if !t.MergedAt.IsZero() {
			step, _ := afterMerge(v, t)
			acts = append(acts, step...)
			continue
		}
		if s.IdleFor(v.Now, StallWait) {
			acts = append(acts, release(t, "its task is done"))
		}
	}
	return acts
}

// afterMerge is a merged task's session's step, and whether it releases the
// session. The merged update gives the session a turn to tear down what it
// provisioned and report it with engine task cleaned. It is released once
// idle after that report, or at CleanupCap after the merge if still in a
// turn; or, without one, once idle CleanupWait after the merge with no limit
// reset pending, or at once when its window is gone, telling the operator.
// One without a report still in a turn CleanupCap after the merge is noticed
// once and released when it goes idle.
//
// A turn stopped on a usage limit is resumed, or alerted, as an open task's
// is, and waits for its reset meanwhile.
func afterMerge(v View, t Item) ([]Action, bool) {
	lim := limit(v, t)
	if len(lim) > 0 && lim[0].Kind == Resume {
		return lim, false
	}
	step, released := releaseAfterMerge(v, t)
	return append(lim, step...), released
}

// releaseAfterMerge is afterMerge's release or cap notice.
func releaseAfterMerge(v View, t Item) ([]Action, bool) {
	s := *t.Session
	since := v.Now.Sub(t.MergedAt)
	busy := s.Live && !s.Idle()
	switch {
	case t.Cleaned != nil && (!busy || since >= CleanupCap):
		return []Action{releaseCleaned(t, busy)}, true
	case t.Cleaned == nil && (!s.Live || s.Idle() && s.ResetsAt <= 0 && since >= CleanupWait):
		a := release(t, "its task is merged")
		return []Action{told(a, "uncleaned/"+t.Current.ID, fmt.Sprintf("%s was released without confirming cleanup after %s merged: %s",
			s.Name, t.ID, where(s)))}, true
	case busy && since >= CleanupCap:
		return []Action{notice(t, "cleanup-busy/"+t.Current.ID, fmt.Sprintf(
			"%s on host %s is still in a turn %s after %s merged, without confirming cleanup. It is released once it goes idle",
			s.Name, s.Host, CleanupCap, t.ID))}, false
	}
	return nil, false
}

// releaseCleaned releases a merged task's session that reported its cleanup.
// One still in a turn is released too, at CleanupCap, telling the operator:
// its report says the protocol is done, and a session whose hooks can no
// longer run, as when its folder is gone, never reads idle.
func releaseCleaned(t Item, busy bool) Action {
	s := *t.Session
	a := release(t, "its task is merged and cleaned up")
	var text []string
	if busy {
		text = append(text, fmt.Sprintf("%s was released still in a turn %s after %s merged, after reporting its cleanup",
			s.Name, CleanupCap, t.ID))
	}
	if t.Cleaned.Left != "" {
		text = append(text, fmt.Sprintf("%s cleaned up after %s merged, but left on host %s: %s",
			s.Name, t.ID, s.Host, t.Cleaned.Left))
	}
	if len(text) == 0 {
		return a
	}
	key := "cleaned/" + t.Current.ID
	if busy {
		key = "cleaned-busy/" + t.Current.ID
	}
	return told(a, key, strings.Join(text, "; "))
}

// where names a session's host, worktree and branch for the operator.
func where(s Session) string {
	return fmt.Sprintf("host %s, worktree %s, branch %s", s.Host, cmp.Or(s.Worktree, "unknown"), cmp.Or(s.Branch, "unknown"))
}

// release closes t's session. One that only a background job keeps busy
// is closed with that job still running, so the operator is told. A done
// task's release also runs the repo's own worktree release.
func release(t Item, why string) Action {
	s := *t.Session
	a := Action{Kind: Release, Item: t.ID, Attempt: t.Current.ID, Session: s, Text: why, Done: t.Kind == "task" && t.State == "done", Head: t.MergedHead}
	if s.Background {
		a.Key = "released/" + t.Current.ID
		a.Text = fmt.Sprintf("%s was released, with a background job still running, because %s", s.Name, why)
	}
	return a
}

// told is release a telling the operator text under key, once, and that a
// background job still ran, if one did.
func told(a Action, key, text string) Action {
	if a.Key != "" {
		text += ", with a background job still running"
	}
	a.Key, a.Text = key, text
	return a
}

// cleanup, once the root is done, releases every session that is idle or
// gone, a merged task's by the cleanup rule, and stops coordinating when
// none is left busy in a turn, limited, unknown, or not yet released after
// its merge.
func cleanup(v View) []Action {
	var acts []Action
	pending := false
	for _, t := range v.Sessions() {
		s := *t.Session
		switch {
		case t.Current.State == "ended":
		case s.Unknown:
			pending = true
		case t.Kind == "task" && !t.MergedAt.IsZero():
			step, released := afterMerge(v, t)
			acts = append(acts, step...)
			pending = pending || !released
		case s.Live && !s.Idle():
			pending = true
		default:
			acts = append(acts, release(t, "the work is done"))
		}
	}
	if !pending {
		acts = append(acts, Action{Kind: Stop, Item: v.Root.ID, Text: v.Root.ID + " is done"})
	}
	return acts
}
