package run

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/tracker"
)

// waitingView is a lone running task whose live session has waited for
// input for StallWait since waitStart, with denial as its newest
// permission log entry.
func waitingView(waitStart time.Time, denial *hostops.Denial) View {
	r := ref(2)
	s := Session{Name: "r-2", Host: "mac", Live: true, Waiting: true, Mode: "hands-off",
		Spawned: waitStart.Add(-time.Hour), WaitingSince: waitStart, Denial: denial}
	return View{Feature: tracker.Feature{Ref: r, Open: true, Tasks: []tracker.Card{{Ref: r, Open: true, Labels: []string{Running}}}},
		TaskOnly: true, Sessions: map[tracker.Ref]Session{r: s}, Now: waitStart.Add(StallWait)}
}

func stallText(t *testing.T, v View) string {
	t.Helper()
	for _, a := range Decide(v) {
		if a.Kind == Stall {
			return a.Text
		}
	}
	t.Fatal("no stall")
	return ""
}

func TestAWaitAfterADenialIsAPermissionStop(t *testing.T) {
	merge := json.RawMessage(`{"command":"gh pr merge 77 -R o/r --merge --match-head-commit ca44dd5","description":"Merge"}`)
	at := func(d time.Duration) *hostops.Denial {
		return &hostops.Denial{At: t0.Add(d), Tool: "Bash", Input: merge, Decision: "denied by auto mode", Reason: "[Merge Without Review]"}
	}
	for _, tc := range []struct {
		name   string
		denial *hostops.Denial
		stop   bool
	}{
		{"no denial", nil, false},
		{"a denial just before the wait was seen", at(-4 * time.Minute), true},
		{"a denial at the lead's edge", at(-DenialLead), true},
		{"a denial after the wait was seen", at(time.Minute), true},
		{"a denial older than the lead", at(-DenialLead - time.Second), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := stallText(t, waitingView(t0, tc.denial))
			if !tc.stop {
				if got != "r-2 has waited for input for 10m0s, outside a hard stop" {
					t.Errorf("text %q", got)
				}
				return
			}
			for _, want := range []string{"Permission hard stop", "Bash `gh pr merge 77 -R o/r --merge --match-head-commit ca44dd5`",
				"denied by auto mode: [Merge Without Review]", "permission rule"} {
				if !strings.Contains(got, want) {
					t.Errorf("text %q lacks %q", got, want)
				}
			}
		})
	}
}

func TestAWaitingPlannerAfterADenialIsAPermissionStop(t *testing.T) {
	f := ref(1)
	planner := Session{Name: "r-1", Live: true, Waiting: true, Spawned: t0.Add(-time.Hour), WaitingSince: t0,
		Denial: &hostops.Denial{At: t0, Tool: "Read", Input: json.RawMessage(`{"file_path":"/etc/hosts"}`), Decision: "deny"}}
	v := View{Feature: tracker.Feature{Ref: f, Open: true, Labels: []string{Approved}}, Sessions: map[tracker.Ref]Session{f: planner}, Now: t0.Add(StallWait)}
	got := stallText(t, v)
	if !strings.Contains(got, "Permission hard stop") || !strings.Contains(got, "Read `{\"file_path\":\"/etc/hosts\"}` was deny") {
		t.Errorf("text %q", got)
	}
}

// A session whose host's status reports a denial as its newest permission
// log entry stalls as a Permission stop, once per wait.
func TestTheRunReadsADenialFromTheHostStatus(t *testing.T) {
	w := newWorld(t, []string{"engine:feature", Executing}, card(2, "standard"))
	w.do[2] = behaviour{claim: 1, waitAt: 2}
	for i := range 15 {
		v := gather(t, w)
		Apply(w, v, Decide(v))
		if e := w.execs[ref(2)]; i == 2 && e != nil {
			e.denial = &hostops.Denial{At: w.now, Tool: "Bash", Input: json.RawMessage(`{"command":"gh pr merge 3"}`), Decision: "denied by auto mode"}
		}
		w.sleep(CheckEvery)
	}
	c := w.comments[ref(2)]
	if len(c) != 1 || !strings.Contains(c[0], "Permission hard stop") || !strings.Contains(c[0], "Bash `gh pr merge 3` was denied by auto mode") {
		t.Errorf("comments %q", c)
	}
}
