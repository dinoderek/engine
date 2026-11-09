package reconcile

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/hostops"
)

// one is v's only action.
func one(t *testing.T, v View) Action {
	t.Helper()
	acts := Decide(v)
	if len(acts) != 1 {
		t.Fatalf("actions:\n%s", shows(acts))
	}
	return acts[0]
}

func TestActionTextsAndMarks(t *testing.T) {
	denied := waiting(StallWait)
	denied.Denial = &hostops.Denial{At: denied.WaitingSince, Tool: "Bash", Input: json.RawMessage(`{"command":"gh pr merge 3"}`), Decision: "denied"}
	for _, tc := range []struct {
		name string
		v    View
		has  []string
		mark func(Action) bool
	}{
		{"a wait after a denial is the Permission stop", view(plan("planned"), with(task(2, "running"), denied)),
			[]string{"Permission hard stop", "Bash `gh pr merge 3` was denied"}, func(a Action) bool { return a.Stalled.Equal(denied.WaitingSince) && a.Attempt == "a2" }},
		{"a wait while signed out names the sign-in", view(plan("planned"), with(task(2, "running"), signedOutWait())),
			[]string{"demo-2 has waited for input for 10m0s, while claude is signed out on devbox", "engine setup --host devbox", "type continue"}, nil},
		{"a planner waiting while signed out keeps its name", view(with(plan("approved"), signedOutWait())),
			[]string{"the planner demo-1 has waited for input for 10m0s, while claude is signed out on devbox"}, nil},
		{"a signed-out wait leads with the sign-in and names the denial", view(plan("planned"), with(task(2, "running"), deniedAndSignedOut())),
			[]string{"while claude is signed out on devbox", "It also follows a denied permission request, the Permission hard stop (hard-stops.md): Bash `gh pr merge 3` was denied"},
			func(a Action) bool { return strings.Index(a.Text, "signed out") < strings.Index(a.Text, "Permission") }},
		{"a placement while signed out names the sign-in", withFleet(view(plan("planned"), task(2, "todo")), signedOut(fleet(2), "claude")),
			[]string{"claude is signed out on devbox (`engine setup --host devbox` names the sign-in)"}, nil},
		{"the planner sign-in notice says it waits", withFleet(view(plan("approved")), signedOut(fleet(2), "claude")),
			[]string{"The planner waits: claude is signed out on devbox", "starts once the harness is signed in"}, nil},
		{"a plain wait says how long", view(plan("planned"), with(task(2, "running"), waiting(StallWait))),
			[]string{"demo-2 has waited for input for 10m0s, outside a hard stop"}, nil},
		{"the claim wait says so", view(plan("planned"), with(task(2, "todo"), working())),
			[]string{"demo-2 has not claimed the task 5m0s after it started"}, func(a Action) bool { return a.Stalled.Equal(t0.Add(-time.Hour)) }},
		{"a far reset is marked far", view(plan("planned"), with(task(2, "running"), limited(48*time.Hour, 0))),
			[]string{"more than 24 hours away"}, func(a Action) bool { return a.Far && a.Limit == unix(48*time.Hour) }},
		{"a stuck limit is marked stuck", view(plan("planned"), with(task(2, "running"), limited(-15*time.Minute, -11*time.Minute))),
			[]string{"still limited 10 minutes after continue; resumed-for="}, func(a Action) bool { return !a.Far && a.Limit == unix(-15*time.Minute) }},
		{"continue names its reset", view(plan("planned"), with(task(2, "running"), limited(-3*time.Minute, 0))),
			[]string{"continue; resumed-for="}, func(a Action) bool { return a.ResetsAt == unix(-3*time.Minute) && a.Session.WindowID == "@1" }},
		{"lint after planning lists the problems", withLint(view(plan("planned"), task(2, "todo"))), []string{"Plan lint failed: [t2: no ## Tests section]"}, nil},
		{"lint at planning says nothing started", withLint(view(plan("approved"), task(2, "todo"))), []string{"no tasks were dispatched"}, nil},
		{"the planner harness notice names it", view(harness(plan("approved"), "codex")), []string{"planner harness codex is not one host devbox runs"}, nil},
		{"the summary lists each task and PR", view(plan("planned"), task(2, "done"), task(3, "done")),
			[]string{"Every task is complete.\nt2: Task 2 \nt3: Task 3 "}, nil},
		{"a request without the lock is told to take it", view(plan("planned"), requesting(with(task(2, "in-review"), working()), false)),
			nil, func(a Action) bool { return strings.Contains(a.Refuse, "does not hold the repository merge lock") }},
		{"a request from a running task is told to be in-review", view(plan("planned"), requesting(with(task(2, "running"), working()), true)),
			nil, func(a Action) bool {
				return strings.Contains(a.Refuse, "The task is running, not in-review") && a.Request.ID == 9
			}},
		{"a dead session's attempt ends with why", view(plan("planned"), with(task(2, "running"), gone())), []string{"its session demo-2 has died"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := one(t, tc.v)
			for _, want := range tc.has {
				if !strings.Contains(a.Text, want) {
					t.Errorf("text %q lacks %q", a.Text, want)
				}
			}
			if tc.mark != nil && !tc.mark(a) {
				t.Errorf("action %+v", a)
			}
		})
	}
}

// A denial logged long before the wait began is not its cause.
func TestAnOldDenialIsAPlainWait(t *testing.T) {
	s := waiting(StallWait)
	s.Denial = &hostops.Denial{At: s.WaitingSince.Add(-DenialLead - time.Second), Tool: "Bash"}
	if WaitText(s, "plain") != "plain" {
		t.Error(WaitText(s, "plain"))
	}
}

// signedOutWait is a claude session waiting for input on a host that says
// claude is signed out there.
func signedOutWait() Session {
	s := waiting(StallWait)
	s.Harness, s.SignedOut = "claude", true
	return s
}

// deniedAndSignedOut is a signed-out wait that also follows a denial.
func deniedAndSignedOut() Session {
	s := signedOutWait()
	s.Denial = &hostops.Denial{At: s.WaitingSince, Tool: "Bash", Input: json.RawMessage(`{"command":"gh pr merge 3"}`), Decision: "denied"}
	return s
}
