package reconcile

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
)

// unix is a stall or limit key's time.
func unix(d time.Duration) int64 { return t0.Add(d).Unix() }

func blocked(t Item, by ...string) Item { t.Blocked = by; return t }

func tier(t Item, tier string) Item { t.Tier = tier; return t }

func requesting(t Item) Item {
	t.Merge = &control.Update{ID: 9, Item: t.ID, Kind: "merge-request", Merge: &control.MergeRequest{PR: "https://github.com/o/r/pull/" + "4"}}
	return t
}

func question(t Item, id int) Item { t.Question = id; return t }

func ended(t Item) Item { t.Current.State = "ended"; return t }

func vanished(t Item, question int) Item {
	t.Vanished, t.Question, t.Resume = true, question, true
	t.Current = control.Attempt{ID: "a" + t.ID[1:], State: "ended", Reason: control.EndVanished}
	return t
}

func withFleet(v View, f Fleet) View { v.Fleet = f; return v }

func withLint(v View) View { v.Lint = []string{"t2: no ## Tests section"}; return v }

func mergedAt(t Item, d time.Duration) Item { t.MergedAt = t0.Add(-d); return t }

func harness(t Item, h string) Item { t.Harness = h; return t }

func limited(resets time.Duration, resumed time.Duration) Session {
	s := working()
	s.Limited, s.ResetsAt = true, unix(resets)
	if resumed != 0 {
		s.ResumeAt = t0.Add(resumed)
	}
	return s
}

func flagged(s Session) Session { s.StallNoted = s.WaitingSince; return s }

// signedOut is f with its first host reporting harnesses signed out.
func signedOut(f Fleet, harnesses ...string) Fleet { f.Hosts[0].Status.SignedOut = harnesses; return f }

// unreachable is f with its first host's status unreadable.
func unreachable(f Fleet) Fleet { f.Hosts[0].Unreachable = "ssh: connect timed out"; return f }

// bothHarnesses is fleet(free) with devbox running claude and codex alike.
func bothHarnesses(free int) Fleet {
	f := fleet(free)
	f.HarnessRatio, f.Caps["codex"] = map[string]int{"claude": 1, "codex": 1}, 4
	f.Hosts[0].Harnesses = []string{"claude", "codex"}
	return f
}

func gone() Session { return Session{Mode: "hands-off", Spawned: t0.Add(-time.Hour)} }

func TestDecide(t *testing.T) {
	spawned := unix(-time.Hour)
	liveElsewhere := fleet(2)
	liveElsewhere.Hosts[0].Live = []string{"demo-2"}
	for _, tc := range []struct {
		name string
		v    View
		want []string
	}{
		// Dispatch order and placement.
		{"most downstream, then deep, first; a blocked task waits", view(plan("executing"), task(2, "todo"), tier(task(3, "todo"), "deep"), blocked(task(4, "todo", "t2"), "t2")),
			[]string{"dispatch t2 devbox claude false", "dispatch t3 devbox claude false"}},
		{"deep starts before standard; one with no free slot waits without a notice", withFleet(view(plan("executing"), task(2, "todo"), tier(task(3, "todo"), "deep")), fleet(1)),
			[]string{"dispatch t3 devbox claude false"}},
		{"a task no host can take is a notice", view(plan("executing"), harness(task(2, "todo"), "codex")), []string{"notice t2 placement/3"}},
		{"a harness signed out on its host is passed over for another", withFleet(view(plan("executing"), task(2, "todo")), signedOut(bothHarnesses(2), "claude")),
			[]string{"dispatch t2 devbox codex false"}},
		{"a task whose only harness is signed out is a notice", withFleet(view(plan("executing"), task(2, "todo")), signedOut(fleet(2), "claude")),
			[]string{"notice t2 placement/3"}},
		{"an unreachable host is never read as signed out", withFleet(view(plan("executing"), task(2, "todo")), unreachable(signedOut(fleet(2), "claude"))), nil},
		{"another harness signed out changes nothing", withFleet(view(plan("executing"), task(2, "todo")), signedOut(fleet(2), "codex")),
			[]string{"dispatch t2 devbox claude false"}},
		{"an unknown session is not dispatched", view(plan("executing"), with(task(2, "todo"), Session{Unknown: true})), nil},
		{"a window live on any host holds a todo task", withFleet(view(plan("executing"), task(2, "todo")), liveElsewhere), nil},

		// Plan states.
		{"an approved plan without tasks starts its planner", view(plan("approved")), []string{"dispatch p1 devbox claude false"}},
		{"the planner waits for a free local slot", withFleet(view(plan("approved")), fleet(0)), nil},
		{"a planner harness the host does not run is a notice", view(harness(plan("approved"), "codex")), []string{"notice p1 planner-harness/2"}},
		{"a planner harness signed out here is a notice, not a start", withFleet(view(plan("approved")), signedOut(fleet(2), "claude")), []string{"notice p1 planner-signin/2/claude"}},
		{"a live planner is left to work", view(with(plan("approved"), working())), nil},
		{"an unknown planner is left alone", view(with(plan("approved"), Session{Unknown: true})), nil},
		{"a planner waiting outside a hard stop is flagged", view(with(plan("approved"), waiting(StallWait))), []string{fmt.Sprintf("notice p1 stall/a1/%d", unix(-StallWait))}},
		{"a planner that ended without tasks is flagged", view(with(plan("approved"), gone())), []string{fmt.Sprintf("notice p1 stall/a1/%d", spawned)}},
		{"a planned plan's lint failure is a notice", withLint(view(plan("approved"), task(2, "todo"))), []string{"notice p1 lint/2"}},
		{"a later plan's lint failure is a notice too", withLint(view(plan("executing"), task(2, "todo"))), []string{"notice p1 lint/2"}},
		{"a plan that passes lint is planned", view(plan("approved"), task(2, "todo")), []string{"state p1 planned"}},
		{"an unapproved plan stops", view(plan("prepare")), []string{"stop p1"}},
		{"the first dispatch makes the plan executing", view(plan("planned"), task(2, "todo")), []string{"dispatch t2 devbox claude false", "state p1 executing"}},
		{"the acceptance task alone makes it verifying", view(plan("executing"), task(2, "done"), task(3, "todo", "t2")),
			[]string{"dispatch t3 devbox claude false", "state p1 verifying"}},
		{"a hard stop makes it blocked", view(plan("executing"), with(task(2, "needs-human"), working()), task(3, "todo")),
			[]string{"dispatch t3 devbox claude false", "state p1 blocked"}},
		{"every task done makes it done, with the summary", view(plan("verifying"), task(2, "done"), task(3, "done")), []string{"state p1 done finished"}},

		// Failed and vanished attempts.
		{"a held task's dead session ends its attempt", view(plan("executing"), with(task(2, "running"), gone())), []string{"end t2 a2"}},
		{"a vanished attempt is dispatched again as a resume", view(plan("executing"), vanished(task(2, "running"), 0)), []string{"dispatch t2 devbox claude true"}},
		{"an operator question parks a vanished task", view(plan("executing"), vanished(task(2, "running"), 7)), nil},

		// Stalls and the claim wait.
		{"a held session waiting outside a hard stop is flagged", view(plan("executing"), with(task(2, "running"), waiting(StallWait))),
			[]string{fmt.Sprintf("notice t2 stall/a2/%d", unix(-StallWait))}},
		{"a flagged wait is not flagged again", view(plan("executing"), with(task(2, "running"), flagged(waiting(StallWait)))), nil},
		{"a short wait is not flagged", view(plan("executing"), with(task(2, "running"), waiting(StallWait-time.Second))), nil},
		{"a wait at a hard stop is not flagged", view(plan("executing"), with(task(2, "needs-human"), waiting(StallWait))), []string{"state p1 blocked"}},
		{"a wait on an open question is not flagged", view(plan("executing"), question(with(task(2, "running"), waiting(StallWait)), 5)), nil},
		{"a wait on the coordinator's merge is not flagged", view(plan("executing"), requesting(with(task(2, "in-review"), waiting(StallWait)))), nil},
		{"a short wait after a long background spell is not flagged", view(plan("executing"), with(task(2, "running"), afterBackground(time.Minute, 2*StallWait))), nil},
		{"a session busy only in a background job is not flagged", view(plan("executing"), with(task(2, "running"), background(StallWait))), nil},
		{"a running task never waits for its claim: the server claims at dispatch", view(plan("executing"), with(task(2, "running"), working())), nil},
		{"a live session of an unheld task is flagged after the claim wait", view(plan("executing"), with(task(2, "todo"), working())),
			[]string{fmt.Sprintf("notice t2 stall/a2/%d", spawned)}},
		{"an unheld task whose session ended is flagged and started again", view(plan("executing"), with(task(2, "todo"), gone())),
			[]string{fmt.Sprintf("notice t2 stall/a2/%d", spawned), "dispatch t2 devbox claude false"}},

		// Usage limits.
		{"a limited session is told to continue after its reset", view(plan("executing"), with(task(2, "running"), limited(-3*time.Minute, 0))), []string{"resume t2 a2"}},
		{"a reset a day away is a notice", view(plan("executing"), with(task(2, "running"), limited(48*time.Hour, 0))),
			[]string{fmt.Sprintf("notice t2 limit/far/a2/%d", unix(48*time.Hour))}},
		{"a session still limited after continue is a notice", view(plan("executing"), with(task(2, "running"), limited(-15*time.Minute, -11*time.Minute))),
			[]string{fmt.Sprintf("notice t2 limit/stuck/a2/%d", unix(-15*time.Minute))}},
		{"a limited session at a hard stop is left alone", view(plan("executing"), with(task(2, "needs-human"), limited(-3*time.Minute, 0))), []string{"state p1 blocked"}},

		// Release.
		{"a done task's waiting session is released", view(plan("executing"), with(task(2, "done"), waiting(StallWait)), task(3, "todo")),
			[]string{"release t2 a2", "dispatch t3 devbox claude false"}},
		{"a done task's session busy only in a background job is released", view(plan("executing"), with(task(2, "done"), background(StallWait)), task(3, "todo")),
			[]string{"release t2 a2", "dispatch t3 devbox claude false"}},
		{"a done task's session busy in a turn is left alone", view(plan("executing"), with(task(2, "done"), working()), task(3, "todo")),
			[]string{"dispatch t3 devbox claude false"}},
		{"a just-merged task keeps its window to report", view(plan("executing"), mergedAt(with(task(2, "done"), waiting(StallWait)), 5*time.Minute), task(3, "todo")),
			[]string{"dispatch t3 devbox claude false"}},

		// A queued merge is decided by DecideQueue, never by the root's pass.
		{"a queued request is left to the merge queue", withLint(view(plan("executing"), requesting(with(task(2, "in-review"), working())))),
			[]string{"notice p1 lint/2"}},

		// Stop, and cleanup once done.
		{"a failed standalone task stops", view(task(5, "failed")), []string{"stop t5"}},
		{"a done plan with no session left stops", view(plan("done"), ended(with(task(2, "done"), gone()))), []string{"stop p1"}},
		{"a done plan waits for a working session", view(plan("done"), with(task(2, "done"), working())), nil},
		{"a done plan releases a waiting session, then stops", view(plan("done"), with(task(2, "done"), waiting(time.Minute))), []string{"release t2 a2", "stop p1"}},
		{"a done plan releases a session busy only in a background job", view(plan("done"), with(task(2, "done"), background(time.Minute))), []string{"release t2 a2", "stop p1"}},
		{"a done plan waits for an unknown session", view(plan("done"), with(task(2, "done"), Session{Unknown: true})), nil},
		{"a done standalone task keeps a just-merged window", view(mergedAt(with(task(5, "done"), waiting(time.Minute)), time.Minute)), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, want := shows(Decide(tc.v)), strings.Join(tc.want, "\n"); got != want {
				t.Errorf("actions:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

// A standalone task and a plan drive the same task the same way; only a
// plan's own state moves.
func TestAStandaloneTaskAndAPlanDriveAlike(t *testing.T) {
	alone := task(5, "approved")
	alone.Plan = ""
	if got := shows(Decide(view(alone))); got != "dispatch t5 devbox claude false" {
		t.Errorf("standalone: %s", got)
	}
	if got := shows(Decide(view(plan("planned"), task(5, "todo")))); got != "dispatch t5 devbox claude false\nstate p1 executing" {
		t.Errorf("plan: %s", got)
	}
	for _, v := range []View{view(with(task(5, "running"), gone())), view(plan("executing"), with(task(5, "running"), gone()))} {
		if got := shows(Decide(v)); got != "end t5 a5" {
			t.Errorf("%s: %s", v.Root.ID, got)
		}
	}
	done := with(task(5, "done"), waiting(time.Minute))
	if got := shows(Decide(view(done))); got != "release t5 a5\nstop t5" {
		t.Errorf("done standalone: %s", got)
	}
}
