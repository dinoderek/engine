package control

import (
	"fmt"
	"testing"
	"time"
)

func TestAWindowDeadAtALimitEndsVanishedEveryTime(t *testing.T) {
	s := service(t)
	repo, plan := repoPlan(t, s)
	task := newTask(t, s, repo.ID, plan.ID)
	approve(t, s, plan)
	// Each session dies at the limit before it claims the task, which would
	// otherwise be an early death and then a failure.
	current := &task
	for i := range 3 {
		_, out := dispatch(t, s, *current)
		launched(t, s, out.Attempt.ID)
		at, err := s.EndAtLimit(out.Attempt.ID, "its session died at a usage limit", 5000)
		if err != nil {
			t.Fatal(err)
		}
		current = call(t, s, op, Request{Op: "get", ID: task.ID}).Item
		if at.Reason != EndVanished || !at.Limited || at.ResetsAt != 5000 || at.Early || current.State == "failed" {
			t.Fatalf("death %d at a limit: %+v, task %s", i+1, at, current.State)
		}
		if snap := st0(t, s); !Vanished(snap.tx(), *current) {
			t.Fatalf("death %d: the task is not dispatched again", i+1)
		}
	}
	// Ending it again changes nothing.
	again, err := s.EndAtLimit(current.Attempt, "again", 9000)
	if err != nil || again.ResetsAt != 5000 {
		t.Errorf("a repeat: %+v, %v", again, err)
	}
}

func st0(t *testing.T, s Service) state {
	t.Helper()
	st, err := s.Store.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestHarnessLimits(t *testing.T) {
	t0 := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	at := func(n int, host string, started, ended time.Duration, limited bool) Attempt {
		a := Attempt{ID: fmt.Sprintf("a%d", n), Item: fmt.Sprintf("t%d", n), Host: host, Harness: "claude", State: "ended",
			Started: t0.Add(started), Ended: t0.Add(ended), Limited: limited}
		if limited {
			a.ResetsAt = int64(n)
		}
		if ended < 0 {
			a.State, a.Ended = "running", time.Time{}
		}
		return a
	}
	cases := []struct {
		name     string
		attempts []Attempt
		wrote    string // an attempt that wrote to the server
		planner  string // an item that is a plan, whose attempts are its planner's
		want     string
	}{
		{name: "no limit", attempts: []Attempt{at(1, "mac", 0, time.Hour, false)}, want: "[]"},
		{name: "a limit", attempts: []Attempt{at(1, "mac", 0, time.Hour, true), at(2, "box", 0, time.Hour, true)},
			want: "[box/claude reset 2 since 1h0m0s ended 1h0m0s a2 probe 0s live false probes 0 mac/claude reset 1 since 1h0m0s ended 1h0m0s a1 probe 0s live false probes 0]"},
		{name: "a probe in flight", attempts: []Attempt{at(1, "mac", 0, time.Hour, true), at(2, "mac", 2*time.Hour, -1, false)},
			want: "[mac/claude reset 1 since 1h0m0s ended 1h0m0s a1 probe 2h0m0s live true probes 1]"},
		{name: "failed probes keep the first limit's start", attempts: []Attempt{at(1, "mac", 0, time.Hour, true), at(2, "mac", 2*time.Hour, 3*time.Hour, true), at(3, "mac", 4*time.Hour, 5*time.Hour, false)},
			want: "[mac/claude reset 2 since 1h0m0s ended 3h0m0s a2 probe 4h0m0s live false probes 2]"},
		{name: "a claim after the limit lifts it", attempts: []Attempt{at(1, "mac", 0, time.Hour, true), at(2, "mac", 2*time.Hour, -1, false)}, wrote: "a2", want: "[]"},
		{name: "a claim before the limit does not", attempts: []Attempt{at(1, "mac", 2*time.Hour, 3*time.Hour, true), at(2, "mac", time.Hour, -1, false)}, wrote: "a2",
			want: "[mac/claude reset 1 since 3h0m0s ended 3h0m0s a1 probe 0s live false probes 0]"},
		{name: "a claim between limits starts the run anew", attempts: []Attempt{at(1, "mac", 0, time.Hour, true), at(2, "mac", 2*time.Hour, 3*time.Hour, false), at(3, "mac", 4*time.Hour, 5*time.Hour, true)}, wrote: "a2",
			want: "[mac/claude reset 3 since 5h0m0s ended 5h0m0s a3 probe 0s live false probes 0]"},
		{name: "a planner's claim does not lift it", attempts: []Attempt{at(1, "mac", 0, time.Hour, true), func() Attempt { a := at(2, "mac", 2*time.Hour, -1, false); a.Item = "p2"; return a }()},
			wrote: "a2", planner: "p2", want: "[mac/claude reset 1 since 1h0m0s ended 1h0m0s a1 probe 0s live false probes 0]"},
		{name: "another harness's limit", attempts: []Attempt{func() Attempt { a := at(1, "mac", 0, time.Hour, true); a.Harness = "codex"; return a }(), at(2, "mac", 2*time.Hour, -1, false)}, wrote: "a2",
			want: "[mac/codex reset 1 since 1h0m0s ended 1h0m0s a1 probe 0s live false probes 0]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := emptyState()
			for _, a := range c.attempts {
				st.Attempts[a.ID] = a
				st.Items[a.Item] = Item{ID: a.Item, Kind: "task"}
			}
			if c.planner != "" {
				st.Items[c.planner] = Item{ID: c.planner, Kind: "plan"}
			}
			if c.wrote != "" {
				st.Updates = append(st.Updates, Update{ID: 1, Author: c.wrote})
			}
			var got []string
			for _, l := range HarnessLimits(st.tx()) {
				probe := time.Duration(0)
				if !l.Probe.IsZero() {
					probe = l.Probe.Sub(t0)
				}
				got = append(got, fmt.Sprintf("%s/%s reset %d since %v ended %v %s probe %v live %v probes %d",
					l.Host, l.Harness, l.ResetsAt, l.Since.Sub(t0), l.Ended.Sub(t0), l.Attempt, probe, l.Live, l.Probes))
			}
			if s := fmt.Sprintf("%v", got); s != c.want {
				t.Errorf("limits\n got %s\nwant %s", s, c.want)
			}
		})
	}
}
