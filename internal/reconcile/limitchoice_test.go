package reconcile

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hostops"
)

// limitedOn is running task n whose live session on host, with harness, is
// at a usage limit resetting resets after t0 (unknown when zero).
func limitedOn(n int, host, harness string, resets time.Duration) Item {
	s := limited(resets, 0)
	if resets == 0 {
		s.ResetsAt = 0
	}
	s.Host, s.Harness = host, harness
	t := with(task(n, "running"), s)
	t.Current.Host, t.Current.Harness = host, harness
	return t
}

// showReads is each read as a line: host, harness, tasks, room and move,
// and whether waiting is recommended.
func showReads(reads []control.LimitRead) string {
	var out []string
	for _, r := range reads {
		line := fmt.Sprintf("%s %s %s", r.Host, r.Harness, strings.Join(r.Tasks, ","))
		switch {
		case r.Room && r.MoveHost != "":
			line += " move " + r.MoveHarness + " on " + r.MoveHost
		case r.Room:
			line += " room"
		default:
			line += " no room"
		}
		if r.Wait {
			line += " wait"
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func TestALimitedSessionWithRoomElsewhereOffersOneMove(t *testing.T) {
	cases := map[string]struct {
		fleet func(Fleet) Fleet
		task  Item
		want  string
	}{
		// The ratio ties, so the default harness goes first.
		"another host": {func(f Fleet) Fleet { return f }, limitedOn(2, "devbox", "claude", time.Hour), "devbox claude t2 move claude on mac wait"},
		"another harness on its host": {func(f Fleet) Fleet { f.Hosts[1].FreeSlots = 0; return f },
			limitedOn(2, "devbox", "claude", time.Hour), "devbox claude t2 move codex on devbox wait"},
		"no free slot anywhere": {func(f Fleet) Fleet { f.Hosts[0].FreeSlots, f.Hosts[1].FreeSlots = 0, 0; return f },
			limitedOn(2, "devbox", "claude", time.Hour), "devbox claude t2 no room wait"},
		"only its own pair": {func(f Fleet) Fleet { f.Hosts = f.Hosts[:1]; f.HarnessRatio = map[string]int{"claude": 1}; return f },
			limitedOn(2, "devbox", "claude", time.Hour), "devbox claude t2 no room wait"},
		"a pinned harness keeps it": {func(f Fleet) Fleet { f.Hosts[1].FreeSlots = 0; return f },
			harness(limitedOn(2, "devbox", "claude", time.Hour), "claude"), "devbox claude t2 no room wait"},
		"the other harness at its cap": {func(f Fleet) Fleet { f.Hosts[1].FreeSlots, f.Caps["codex"] = 0, 0; return f },
			limitedOn(2, "devbox", "claude", time.Hour), "devbox claude t2 no room wait"},
		"its own harness at its cap": {func(f Fleet) Fleet { f.Caps["claude"] = 0; return f },
			limitedOn(2, "devbox", "claude", time.Hour), "devbox claude t2 move claude on mac wait"},
		"the other host paused": {func(f Fleet) Fleet { f.HarnessRatio = map[string]int{"claude": 1}; f.Hosts[1].Paused = true; return f },
			limitedOn(2, "devbox", "claude", time.Hour), "devbox claude t2 no room wait"},
		"the other host at a recorded limit": {func(f Fleet) Fleet {
			f.HarnessRatio, f.Now = map[string]int{"claude": 1}, t0
			f.Limits = []control.HarnessLimit{{Host: "mac", Harness: "claude", ResetsAt: unix(time.Hour), Ended: t0, Since: t0}}
			return f
		}, limitedOn(2, "devbox", "claude", time.Hour), "devbox claude t2 no room wait"},
	}
	for name, c := range cases {
		f := c.fleet(moveHosts())
		f.Now = t0
		if got := showReads(LimitChoices(f, []Item{c.task})); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
}

func TestOnlyALiveLimitedTaskSessionAtNoHardStopIsOffered(t *testing.T) {
	f := moveHosts()
	f.Now = t0
	question := limitedOn(5, "devbox", "claude", time.Hour)
	question.Question = 9
	stopped := limitedOn(6, "devbox", "claude", time.Hour)
	stopped.Stopped = true
	unknown := limitedOn(7, "devbox", "claude", time.Hour)
	unknown.Session.Unknown = true
	notLimited := with(task(8, "running"), working())
	waitingOnReset := limitedOn(9, "mac", "codex", time.Hour)
	waitingOnReset.Session.Limited, waitingOnReset.Session.Waiting = false, true
	tasks := []Item{
		limitedOn(2, "devbox", "claude", time.Hour), // offered
		with(task(3, "needs-human"), limited(time.Hour, 0)),
		with(task(4, "done"), limited(time.Hour, 0)),
		question, stopped, unknown, notLimited, waitingOnReset,
	}
	want := "devbox claude t2 move claude on mac wait\nmac codex t9 move claude on mac wait"
	if got := showReads(LimitChoices(f, tasks)); got != want {
		t.Errorf("%q, want %q", got, want)
	}
	// A plan's planner is never offered a move.
	p := plan("approved")
	p.Session = tasks[0].Session
	p.Current = tasks[0].Current
	if got := LimitChoices(f, []Item{p}); len(got) != 0 {
		t.Errorf("planner: %+v", got)
	}
}

func TestSeveralLimitedSessionsOnOneHarnessShareOneChoice(t *testing.T) {
	f := moveHosts()
	f.Now = t0
	tasks := []Item{limitedOn(4, "devbox", "claude", 90*time.Minute), limitedOn(2, "devbox", "claude", time.Hour)}
	if got, want := showReads(LimitChoices(f, tasks)), "devbox claude t2,t4 room wait"; got != want {
		t.Errorf("%q, want %q", got, want)
	}
	// Only another host counts as room: a host move sends them off devbox.
	f.Hosts[1].FreeSlots = 0
	if got, want := showReads(LimitChoices(f, tasks)), "devbox claude t2,t4 no room wait"; got != want {
		t.Errorf("no room off devbox: %q, want %q", got, want)
	}
	// A pair with a limited session of its own is no room either.
	f.Hosts[1].FreeSlots = 1
	tasks = append(tasks, limitedOn(3, "mac", "claude", time.Hour), limitedOn(5, "mac", "codex", time.Hour))
	got := showReads(LimitChoices(f, tasks))
	want := "devbox claude t2,t4 no room wait\nmac claude t3 move codex on devbox wait\nmac codex t5 move codex on devbox wait"
	if got != want {
		t.Errorf("every pair on mac limited: %q, want %q", got, want)
	}
}

func TestTheRecommendationFollowsTheResetsDistance(t *testing.T) {
	f := moveHosts()
	f.Now = t0
	cases := map[string]struct {
		tasks []Item
		wait  bool
	}{
		"an hour away":            {[]Item{limitedOn(2, "devbox", "claude", time.Hour)}, true},
		"just under two hours":    {[]Item{limitedOn(2, "devbox", "claude", LimitWaitUnder-time.Minute)}, true},
		"two hours away":          {[]Item{limitedOn(2, "devbox", "claude", LimitWaitUnder)}, false},
		"a day away":              {[]Item{limitedOn(2, "devbox", "claude", 25*time.Hour)}, false},
		"unknown":                 {[]Item{limitedOn(2, "devbox", "claude", 0)}, false},
		"several, the latest far": {[]Item{limitedOn(2, "devbox", "claude", time.Hour), limitedOn(3, "devbox", "claude", 3*time.Hour)}, false},
		"several, one unknown":    {[]Item{limitedOn(2, "devbox", "claude", time.Hour), limitedOn(3, "devbox", "claude", 0)}, false},
	}
	for name, c := range cases {
		got := LimitChoices(f, c.tasks)
		if len(got) != 1 || got[0].Wait != c.wait {
			t.Errorf("%s: %+v, want wait %v", name, got, c.wait)
		}
	}
	if got := LimitChoices(f, []Item{limitedOn(2, "devbox", "claude", 3*time.Hour)}); got[0].ResetsAt != unix(3*time.Hour) {
		t.Errorf("reset %d", got[0].ResetsAt)
	}
}

// With no room elsewhere, a limited session keeps today's limit steps; a
// question covering it stands in for the far-reset notice alone.
func TestALimitQuestionStandsInForTheFarResetNotice(t *testing.T) {
	far := with(task(2, "running"), limited(48*time.Hour, 0))
	want := fmt.Sprintf("notice t2 limit/far/a2/%d", unix(48*time.Hour))
	if got := shows(Decide(view(plan("planned"), far))); got != want {
		t.Errorf("no question: %q, want %q", got, want)
	}
	far.Session.LimitAsked = true
	if got := shows(Decide(view(plan("planned"), far))); got != "" {
		t.Errorf("with a question: %q", got)
	}
	// The reset passing still types continue, question or not.
	due := with(task(2, "running"), limited(-ResumeDelay, 0))
	due.Session.LimitAsked = true
	if got := shows(Decide(view(plan("planned"), due))); got != "resume t2 a2" {
		t.Errorf("at the reset: %q", got)
	}
}

// A window at a limit is no room whoever its session is: a planner's, a
// held task's, or one of a plan this pass does not offer.
func TestAnyLimitedWindowIsNoRoom(t *testing.T) {
	for name, w := range map[string]hostops.Window{
		"limited":                {Name: "demo-1", Alive: true, Harness: "claude", State: "limited"},
		"waiting on a reset":     {Name: "demo-1", Alive: true, Harness: "claude", State: "waiting", ResetsAt: unix(time.Hour)},
		"another harness":        {Name: "demo-1", Alive: true, Harness: "codex", State: "limited"},
		"waiting with no reset":  {Name: "demo-1", Alive: true, Harness: "claude", State: "waiting"},
		"dead with a limit seen": {Name: "demo-1", Harness: "claude", State: "limited"},
	} {
		f := moveHosts()
		f.Now, f.HarnessRatio = t0, map[string]int{"claude": 1}
		f.Hosts[1].Status.Windows = []hostops.Window{w}
		want := "devbox claude t2 move claude on mac wait"
		if name == "limited" || name == "waiting on a reset" {
			want = "devbox claude t2 no room wait"
		}
		if got := showReads(LimitChoices(f, []Item{limitedOn(2, "devbox", "claude", time.Hour)})); got != want {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
}
