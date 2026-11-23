package control

import (
	"strings"
	"testing"
	"time"
)

// lossFixture is a plan with two tasks dispatched to devbox and launched,
// both seen live, and the records the loss tests read.
type lossFixture struct {
	s      Service
	a1, a2 string
	t1, t2 Item
}

func newLossFixture(t *testing.T) lossFixture {
	t.Helper()
	s := service(t)
	repo, plan := repoPlan(t, s)
	t1, t2 := newTask(t, s, repo.ID, plan.ID), newTask(t, s, repo.ID, plan.ID)
	approve(t, s, plan)
	_, d1 := dispatch(t, s, t1)
	_, d2 := dispatch(t, s, t2)
	for _, id := range []string{d1.Attempt.ID, d2.Attempt.ID} {
		launched(t, s, id)
		seenLive(t, s, id)
	}
	return lossFixture{s: s, a1: d1.Attempt.ID, a2: d2.Attempt.ID, t1: t1, t2: t2}
}

// windows says which attempts still have a window.
func windows(ids ...string) func(*State, Attempt) bool {
	return func(_ *State, at Attempt) bool {
		for _, id := range ids {
			if at.ID == id {
				return true
			}
		}
		return false
	}
}

func hostNotices(st State) []string {
	var out []string
	for _, u := range st.Updates {
		if u.Item == "" && u.Recipient == "operator" {
			out = append(out, u.Body)
		}
	}
	return out
}

func TestSawTmuxMarksAHostWideLossOnceAndItsDeathsSkipTheCap(t *testing.T) {
	f := newLossFixture(t)
	s := f.s
	// The first read only saves the start time.
	if n, err := s.SawTmux("devbox", TmuxRead{Known: true, Start: 1000}, windows(f.a1, f.a2)); err != nil || n != 0 {
		t.Fatalf("first read: %d, %v", n, err)
	}
	taskWrote(t, s, f.a1, f.t1)
	n, err := s.SawTmux("devbox", TmuxRead{Known: true, Start: 2000}, windows())
	if err != nil || n != 2 {
		t.Fatalf("the loss marked %d attempts, %v", n, err)
	}
	if again, _ := s.SawTmux("devbox", TmuxRead{Known: true, Start: 2000}, windows()); again != 0 {
		t.Fatalf("a second pass marked %d", again)
	}
	st, _ := s.Store.Snapshot()
	if got := hostNotices(st); len(got) != 1 || got[0] != "devbox's tmux restarted; 2 tasks will resume" {
		t.Fatalf("notices: %q", got)
	}
	if st.Hosts["devbox"].TmuxStart != 2000 {
		t.Fatalf("saved start %d", st.Hosts["devbox"].TmuxStart)
	}
	claimedEnd, err := s.EndDead(f.a1, "gone", "claimed tail")
	if err != nil {
		t.Fatal(err)
	}
	unclaimedEnd, err := s.EndDead(f.a2, "gone", "unclaimed tail")
	if err != nil {
		t.Fatal(err)
	}
	st, _ = s.Store.Snapshot()
	if claimedEnd.Reason != EndVanished || claimedEnd.Crashed || claimedEnd.Tail != "" || st.Items[f.t1.ID].State != "running" {
		t.Errorf("claimed: %+v, task %s", claimedEnd, st.Items[f.t1.ID].State)
	}
	if unclaimedEnd.Reason != EndEarlyDeath || unclaimedEnd.Early || unclaimedEnd.Tail != "unclaimed tail" || st.Items[f.t2.ID].State != "todo" {
		t.Errorf("unclaimed: %+v, task %s", unclaimedEnd, st.Items[f.t2.ID].State)
	}
	if !Vanished(&st, st.Items[f.t1.ID]) || !Vanished(&st, st.Items[f.t2.ID]) {
		t.Error("a host-wide death did not leave its task to be dispatched again")
	}
}

func taskWrote(t *testing.T, s Service, attempt string, task Item) {
	t.Helper()
	call(t, s, Actor{Name: attempt, Role: "worker", Attempt: attempt}, Request{Op: "say", ID: task.ID, Body: "started"})
}

// unsee forgets that a pass saw the attempts' windows live.
func unsee(t *testing.T, s Service, ids ...string) {
	t.Helper()
	err := s.Store.Transaction(func(st *State) error {
		for _, id := range ids {
			at := st.Attempts[id]
			at.LiveAt = time.Time{}
			st.Attempts[id] = at
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSawTmuxIsNoLossWhenNothingSaysTheHostLostItsServer(t *testing.T) {
	cases := []struct {
		name  string
		saved int64
		read  TmuxRead
		up    func(f lossFixture) func(*State, Attempt) bool
		seen  bool
	}{
		{"every window gone but the start time unchanged: per-task deaths", 1000, TmuxRead{Known: true, Start: 1000},
			func(lossFixture) func(*State, Attempt) bool { return windows() }, true},
		{"a new start time with one window still up", 1000, TmuxRead{Known: true, Start: 2000},
			func(f lossFixture) func(*State, Attempt) bool { return windows(f.a1) }, true},
		{"no start time was saved to compare", 0, TmuxRead{Known: true, Start: 2000},
			func(lossFixture) func(*State, Attempt) bool { return windows() }, true},
		{"no tmux server and no attempt last seen live: tmux exits after its last window", 1000, TmuxRead{},
			func(lossFixture) func(*State, Attempt) bool { return windows() }, false},
		{"no tmux server but a window is up", 1000, TmuxRead{},
			func(f lossFixture) func(*State, Attempt) bool { return windows(f.a2) }, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newLossFixture(t)
			if !c.seen {
				unsee(t, f.s, f.a1, f.a2)
			}
			if c.saved != 0 {
				if _, err := f.s.SawTmux("devbox", TmuxRead{Known: true, Start: c.saved}, windows(f.a1, f.a2)); err != nil {
					t.Fatal(err)
				}
			}
			n, err := f.s.SawTmux("devbox", c.read, c.up(f))
			st, _ := f.s.Store.Snapshot()
			if err != nil || n != 0 || len(hostNotices(st)) != 0 || st.Attempts[f.a1].HostLost || st.Attempts[f.a2].HostLost {
				t.Fatalf("a loss: %d %v, notices %q, %+v", n, err, hostNotices(st), st.Attempts)
			}
			if c.read.Known && st.Hosts["devbox"].TmuxStart != c.read.Start {
				t.Errorf("the start time was not saved: %+v", st.Hosts)
			}
		})
	}
}

func TestSawTmuxLeavesADispatchInFlightAlone(t *testing.T) {
	f := newLossFixture(t)
	err := f.s.Store.Transaction(func(st *State) error {
		at := st.Attempts[f.a2]
		at.State = "dispatching"
		st.Attempts[at.ID] = at
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	n, err := f.s.SawTmux("devbox", TmuxRead{}, windows())
	st, _ := f.s.Store.Snapshot()
	if err != nil || n != 1 || st.Attempts[f.a2].HostLost || !st.Attempts[f.a1].HostLost {
		t.Fatalf("%d %v %+v", n, err, st.Attempts)
	}
}

func TestSawTmuxLeavesAnotherHostsAttemptsAlone(t *testing.T) {
	f := newLossFixture(t)
	n, err := f.s.SawTmux("elsewhere", TmuxRead{}, windows())
	st, _ := f.s.Store.Snapshot()
	if err != nil || n != 0 || st.Attempts[f.a1].HostLost || len(st.Hosts) != 0 {
		t.Fatalf("%d %v %+v", n, err, st.Hosts)
	}
}

func TestSawTmuxWithNoServerEndsEveryAttemptLastSeenLive(t *testing.T) {
	f := newLossFixture(t)
	if n, err := f.s.SawTmux("devbox", TmuxRead{}, windows()); err != nil || n != 2 {
		t.Fatalf("%d, %v", n, err)
	}
	st, _ := f.s.Store.Snapshot()
	if got := hostNotices(st); len(got) != 1 || !strings.Contains(got[0], "2 tasks will resume") {
		t.Fatalf("notices: %q", got)
	}
}

func TestTheSavedTmuxStartTimeSurvivesAStoreReopen(t *testing.T) {
	s := service(t)
	if _, err := s.SawTmux("devbox", TmuxRead{Known: true, Start: 1234}, windows()); err != nil {
		t.Fatal(err)
	}
	path := s.Store.path
	if err := s.Store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	st, err := reopened.Snapshot()
	if err != nil || st.Hosts["devbox"].TmuxStart != 1234 {
		t.Fatalf("%+v %v", st.Hosts, err)
	}
}
