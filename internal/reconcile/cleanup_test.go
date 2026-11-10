package reconcile

import (
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
)

// cleaned is t with a cleanup report, naming left if set.
func cleaned(t Item, left string) Item {
	t.Cleaned = &control.Update{Kind: control.Cleaned, Left: left}
	return t
}

// at is s run in a worktree on a branch, as its host lists the run.
func at(s Session) Session {
	s.Worktree, s.Branch = "/w/demo-2", "task/demo-2"
	return s
}

// A merged task's session gets a turn to clean up. It is released once idle
// after reporting it, or without a report once idle CleanupWait after the
// merge or once its window is gone; a turn running past CleanupCap without
// a report is noticed. A done plan uses the same rule.
func TestAfterMerge(t *testing.T) {
	merged := func(s Session, d time.Duration) Item { return mergedAt(with(task(2, "done"), at(s)), d) }
	limited := waiting(time.Minute)
	limited.Limited = true
	for _, tc := range []struct {
		name string
		v    View
		want []string
	}{
		{"cleaned and idle: released at once", view(plan("planned"), cleaned(merged(waiting(0), time.Minute), "")), []string{"release t2 a2"}},
		{"cleaned with leftovers: released, telling the operator", view(plan("planned"), cleaned(merged(waiting(0), time.Minute), "a volume")), []string{"release t2 a2 cleaned/a2"}},
		{"cleaned, still in its turn: waits", view(plan("planned"), cleaned(merged(working(), CleanupCap), "")), nil},
		{"cleaned, window gone: released", view(plan("planned"), cleaned(merged(gone(), time.Minute), "")), []string{"release t2 a2"}},
		{"not cleaned, idle, within the wait: waits", view(plan("planned"), merged(waiting(StallWait), CleanupWait-time.Second)), nil},
		{"not cleaned, idle at the wait: released, telling the operator", view(plan("planned"), merged(waiting(0), CleanupWait)), []string{"release t2 a2 uncleaned/a2"}},
		{"not cleaned, window gone: released at once, telling the operator", view(plan("planned"), merged(gone(), 0)), []string{"release t2 a2 uncleaned/a2"}},
		{"not cleaned, in a turn before the cap: waits", view(plan("planned"), merged(working(), CleanupCap-time.Second)), nil},
		{"not cleaned, in a turn at the cap: noticed, not released", view(plan("planned"), merged(working(), CleanupCap)), []string{"notice t2 cleanup-busy/a2"}},
		{"not cleaned, limited at the cap: noticed", view(plan("planned"), merged(limited, CleanupCap)), []string{"notice t2 cleanup-busy/a2"}},
		{"released already: nothing", view(plan("planned"), ended(merged(gone(), time.Hour))), nil},
		{"unknown: nothing", view(plan("planned"), merged(Session{Unknown: true}, time.Hour)), nil},
		{"a done plan releases a cleaned session, then stops", view(plan("done"), cleaned(merged(waiting(0), time.Minute), "")), []string{"release t2 a2", "stop p1"}},
		{"a done plan waits for a merged session to clean up", view(plan("done"), merged(waiting(0), time.Minute)), nil},
		{"a done plan keeps coordinating past the cap", view(plan("done"), merged(working(), CleanupCap)), []string{"notice t2 cleanup-busy/a2"}},
		{"a done plan releases a gone merged session, then stops", view(plan("done"), merged(gone(), 0)), []string{"release t2 a2 uncleaned/a2", "stop p1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			acts := Decide(tc.v)
			var got []string
			for _, a := range acts {
				if a.Item != "t2" && a.Kind != Stop {
					continue // the planned plan's own step once its tasks are done
				}
				line := show(a)
				if a.Kind == Release && a.Key != "" {
					line += " " + a.Key
				}
				got = append(got, line)
			}
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Errorf("actions:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
			}
		})
	}
}

// The operator's notices name what to look at, and a done task's release
// runs the repo's own teardown.
func TestAfterMergeTexts(t *testing.T) {
	first := func(v View) []Action { return Decide(v)[:1] }
	uncleaned := first(view(plan("planned"), mergedAt(with(task(2, "done"), at(background(0))), CleanupWait)))
	if !uncleaned[0].Done || uncleaned[0].Text !=
		"demo-2 was released without confirming cleanup after t2 merged: host devbox, worktree /w/demo-2, branch task/demo-2, with a background job still running" {
		t.Errorf("uncleaned release %+v", uncleaned)
	}
	left := first(view(plan("planned"), cleaned(mergedAt(with(task(2, "done"), waiting(0)), time.Minute), "a volume")))
	if len(left) != 1 || left[0].Text != "demo-2 cleaned up after t2 merged, but left on host devbox: a volume" {
		t.Errorf("left release %+v", left)
	}
	busy := first(view(plan("planned"), mergedAt(with(task(2, "done"), working()), CleanupCap)))
	if len(busy) != 1 || !strings.Contains(busy[0].Text, "demo-2 on host devbox is still in a turn 30m0s after t2 merged") {
		t.Errorf("cap notice %+v", busy)
	}
	unknown := first(view(plan("planned"), mergedAt(with(task(2, "done"), gone()), 0)))
	if len(unknown) != 1 || !strings.Contains(unknown[0].Text, "worktree unknown, branch unknown") {
		t.Errorf("a release with no run listed %+v", unknown)
	}
	planner := first(view(with(plan("done"), waiting(0))))
	if len(planner) != 1 || planner[0].Kind != Release || planner[0].Done {
		t.Errorf("the planner's release %+v, want one that is not a done task's", planner)
	}
}
