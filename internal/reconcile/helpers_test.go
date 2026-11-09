package reconcile

import (
	"fmt"
	"strings"
	"time"

	"github.com/dinoderek/engine/internal/control"
)

var t0 = time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)

// fleet is one host, devbox, running claude with free slots.
func fleet(free int) Fleet {
	return Fleet{Local: "devbox", DefaultHarness: "claude", HarnessRatio: map[string]int{"claude": 1}, Caps: map[string]int{"claude": 4},
		Hosts: []HostLoad{{Name: "devbox", Harnesses: []string{"claude"}, FreeSlots: free, FreeHeavySlots: free}}}
}

// task is plan p1's task n in state, depending on deps.
func task(n int, state string, deps ...string) Item {
	return Item{Item: control.Item{ID: fmt.Sprintf("t%d", n), Kind: "task", Plan: "p1", Repo: "r1", Title: fmt.Sprintf("Task %d", n), State: state, Version: 3, Depends: deps},
		Name: fmt.Sprintf("demo-%d", n)}
}

func plan(state string) Item {
	return Item{Item: control.Item{ID: "p1", Kind: "plan", Repo: "r1", State: state, Version: 2}, Name: "demo-1"}
}

// with gives t a current attempt a<n> and that attempt's session.
func with(t Item, s Session) Item {
	t.Current = control.Attempt{ID: "a" + t.ID[1:], Item: t.ID, Host: "devbox", State: "running", Started: s.Spawned}
	t.Attempt = t.Current.ID
	if s.Name == "" {
		s.Name = t.Name
	}
	if s.Host == "" {
		s.Host = "devbox"
	}
	t.Session = &s
	return t
}

// working is a live session started an hour before t0.
func working() Session {
	return Session{Live: true, WindowID: "@1", Mode: "hands-off", Spawned: t0.Add(-time.Hour)}
}

// waiting is a live session waiting for input since d before t0.
func waiting(d time.Duration) Session {
	s := working()
	s.Waiting, s.WaitingSince = true, t0.Add(-d)
	return s
}

func view(root Item, tasks ...Item) View {
	if root.Kind == "task" {
		tasks = []Item{root}
	}
	return View{Now: t0, Root: root, Tasks: tasks, Fleet: fleet(2)}
}

// show is one action as a line: its kind, item and what tells it apart.
func show(a Action) string {
	parts := []string{string(a.Kind), a.Item}
	switch a.Kind {
	case Dispatch:
		parts = append(parts, a.Host, a.Harness, fmt.Sprint(a.Resume))
	case SetState:
		parts = append(parts, a.State, a.Key)
	case Notice:
		parts = append(parts, a.Key)
	case EndAttempt, Resume, Release:
		parts = append(parts, a.Attempt)
	case Merge:
		parts = append(parts, fmt.Sprint(a.Request.ID))
		if a.Refuse != "" {
			parts = append(parts, "refused")
		}
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func shows(acts []Action) string {
	var out []string
	for _, a := range acts {
		out = append(out, show(a))
	}
	return strings.Join(out, "\n")
}
