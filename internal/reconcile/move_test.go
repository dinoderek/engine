package reconcile

import (
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hostops"
)

// moved is task n in state whose current attempt a<n>, on devbox, ended by
// the operator's move to host and harness (either may be empty).
func moved(n int, state, host, harness string) Item {
	t := task(n, state)
	t.Vanished, t.Resume = true, true
	t.Current = control.Attempt{ID: "a" + t.ID[1:], Item: t.ID, Host: "devbox", State: "ended", Window: "@1",
		Reason: control.EndMoved, MoveHost: host, MoveHarness: harness}
	t.Attempt = t.Current.ID
	return t
}

// moveHosts is devbox and mac, each running claude and codex with free slots.
func moveHosts() Fleet {
	f := fleet(2)
	f.HarnessRatio, f.Caps = map[string]int{"claude": 1, "codex": 1}, map[string]int{"claude": 4, "codex": 4}
	f.Hosts[0].Harnesses = []string{"claude", "codex"}
	f.Hosts = append(f.Hosts, HostLoad{Name: "mac", Harnesses: []string{"claude", "codex"}, FreeSlots: 1, FreeHeavySlots: 1})
	return f
}

func moveView(tasks ...Item) View {
	v := view(plan("planned"), tasks...)
	v.Fleet = moveHosts()
	return v
}

func TestAMovedTaskIsDispatchedOnlyWhereItsMoveSendsIt(t *testing.T) {
	cases := map[string]struct {
		task Item
		want string
	}{
		// devbox has the most free slots, so only the move keeps it off.
		"no host: anywhere but the old one": {moved(2, "running", "", ""), "dispatch t2 mac claude true"},
		"a named host":                      {moved(2, "in-review", "devbox", ""), "dispatch t2 devbox claude true"},
		"a named harness":                   {moved(2, "running", "", "codex"), "dispatch t2 mac codex true"},
		"both":                              {moved(2, "running", "mac", "codex"), "dispatch t2 mac codex true"},
	}
	for name, c := range cases {
		if got := shows(Decide(moveView(c.task))); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
}

func TestAMovedTaskWaitsWhenItsHostCannotTakeIt(t *testing.T) {
	v := moveView(moved(2, "running", "mac", ""))
	v.Fleet.Hosts[1].FreeSlots = 0
	if got := shows(Decide(v)); got != "" {
		t.Errorf("with no slot on mac: %q", got)
	}
	// A host that is not in hosts.toml can never take it: the operator is told.
	v = moveView(moved(2, "running", "gone", ""))
	if got := shows(Decide(v)); got != "notice t2 placement/3" {
		t.Errorf("moved to an unknown host: %q", got)
	}
}

func TestAMovedTasksOldWindowIsReleasedBeforeItsNextDispatch(t *testing.T) {
	for name, s := range map[string]Session{"live": working(), "dead window": {DeadWindow: true, Spawned: t0.Add(-time.Hour)}} {
		s.Name, s.Host = "demo-2", "devbox"
		mv := moved(2, "running", "", "")
		mv.Strays = []Stray{{Attempt: mv.Current, Session: s}}
		acts := Decide(moveView(mv))
		if len(acts) != 1 || acts[0].Kind != Release || !acts[0].Moved || acts[0].Stopped || acts[0].Done || acts[0].Attempt != "a2" || acts[0].Session.Host != "devbox" {
			t.Errorf("%s: actions %+v", name, acts)
		}
	}
}

// Only a read of the old host shows its window gone: until then the task
// waits, unless the operator moved it while that host could not be read.
func TestAMovedTaskWaitsForItsOldHostUnlessMovedBlind(t *testing.T) {
	v := moveView(moved(2, "running", "mac", ""))
	v.Fleet.Hosts[0] = HostLoad{Name: "devbox", Unreachable: "ssh: exit 255"}
	// The operator is told, once per move (the key), how to send it on.
	acts := Decide(v)
	if got := shows(acts); got != "notice t2 move-wait/a2" || !strings.Contains(acts[0].Text, "engine task move t2 --yes") {
		t.Errorf("with devbox unreachable: %q, %+v", got, acts)
	}
	v.Fleet.Hosts[0] = HostLoad{Name: "devbox", Harnesses: []string{"claude"}, Status: statusWithRunsError()}
	if got := shows(Decide(v)); got != "notice t2 move-wait/a2" {
		t.Errorf("with devbox's runs unreadable: %q", got)
	}
	blind := moved(2, "running", "mac", "")
	blind.Current.MovedBlind = true
	v.Tasks = []Item{blind}
	if got := shows(Decide(v)); got != "dispatch t2 mac claude true" {
		t.Errorf("a blind move: %q", got)
	}
}

// A blind move's old window, seen again once its host answers, is released
// while the task runs elsewhere.
func TestAStrayWindowIsReleasedAfterTheTaskMoved(t *testing.T) {
	now := with(task(2, "running"), working())
	now.Current.ID, now.Current.Host, now.Session.Host = "a5", "mac", "mac"
	old := Session{Name: "demo-2", Host: "devbox", Live: true, WindowID: "@1", Spawned: t0.Add(-2 * time.Hour)}
	now.Strays = []Stray{{Attempt: control.Attempt{ID: "a2", Item: "t2", Host: "devbox", Reason: control.EndMoved, MovedBlind: true}, Session: old}}
	if got := shows(Decide(moveView(now))); got != "release t2 a2" {
		t.Errorf("actions %q", got)
	}
}

// Once the plan is done, a window a move left is still released, and
// coordination goes on while one may be left on a host that cannot be read.
func TestADonePlanReleasesWhatAMoveLeftBeforeItStops(t *testing.T) {
	stray := task(2, "done")
	stray.Strays = []Stray{{Attempt: control.Attempt{ID: "a2", Item: "t2", Host: "devbox", Reason: control.EndMoved, MovedBlind: true},
		Session: Session{Name: "demo-2", Host: "devbox", Live: true, WindowID: "@1"}}}
	v := moveView(stray)
	v.Root = plan("done")
	if got := shows(Decide(v)); got != "release t2 a2" {
		t.Errorf("with a stray window: %q", got)
	}
	unread := task(2, "done")
	unread.StrayUnknown = true
	v.Tasks = []Item{unread}
	if got := shows(Decide(v)); got != "" {
		t.Errorf("with a stray's host unread: %q", got)
	}
	v.Tasks = []Item{task(2, "done")}
	if got := shows(Decide(v)); got != "stop p1" {
		t.Errorf("with nothing left: %q", got)
	}
}

func statusWithRunsError() (s hostops.Status) {
	s.RunsError = "permission denied"
	return s
}
