package run

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/tracker"
)

// The session check across hosts: a task that needs linux runs on the
// devbox, which this host, the Mac, reads only through its status.

// onDevbox is a feature whose task 2 runs on the devbox as do says, and
// whose task 3 waits for it.
func onDevbox(t *testing.T, do behaviour) *world {
	t.Helper()
	w := newWorld(t, []string{"engine:feature", Executing}, needs(2, "standard", "linux"), card(3, "standard", 2))
	twoHosts(w, 3, 3)
	w.do[2] = do
	passOf(t, w)
	if !slices.Equal(w.placed, []string{"2@devbox"}) {
		t.Fatalf("placed %v", w.placed)
	}
	return w
}

// checks runs n minutes of passes, one a minute.
func checks(t *testing.T, w *world, n int) {
	t.Helper()
	for range n {
		w.sleep(CheckEvery)
		passOf(t, w)
	}
}

func noteOf(t *testing.T, w *world, n int, note string) bool {
	t.Helper()
	dir, _ := w.NoteDir(ref(n))
	_, ok, err := session.ReadNote(dir, note)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func TestARemoteSessionThatWorksIsLeftAlone(t *testing.T) {
	w := onDevbox(t, behaviour{claim: 1})
	checks(t, w, 20)
	s := gather(t, w).Sessions[ref(2)]
	if s.Host != "devbox" || !s.Live || s.Waiting || s.Unknown {
		t.Errorf("session %+v", s)
	}
	if len(w.comments[ref(2)]) != 0 || len(w.killed) != 0 || len(w.starts) != 1 {
		t.Errorf("comments %q, killed %v, starts %v", w.comments[ref(2)], w.killed, w.starts)
	}
}

func TestARemoteSessionWaitingIsNotedHereAndStallsOnce(t *testing.T) {
	w := onDevbox(t, behaviour{claim: 1, waitAt: 2})
	checks(t, w, 2)
	if !noteOf(t, w, 2, WaitingSince) {
		t.Fatal("no waiting-since in the coordinator's note directory")
	}
	runs, err := filepath.Glob(filepath.Join(w.hostHome("devbox"), ".local/share/engine/runs", "*", "*"))
	if err != nil || len(runs) == 0 {
		t.Fatalf("the devbox's run files %v, %v", runs, err)
	}
	for _, f := range runs {
		if filepath.Base(f) == WaitingSince {
			t.Errorf("waiting-since in the run directory: %s", f)
		}
	}
	checks(t, w, 15)
	c := w.comments[ref(2)]
	if len(c) != 1 || !strings.Contains(c[0], "waited for input") || !strings.Contains(c[0], "Do you want to proceed?") ||
		!strings.Contains(c[0], "engine attach r-2") {
		t.Errorf("comments %q", c)
	}
	// It works again: the note is cleared.
	w.execs[ref(2)].do.waitAt = 0
	w.execs[ref(2)].status = session.AgentBusy
	checks(t, w, 1)
	if noteOf(t, w, 2, WaitingSince) {
		t.Error("waiting-since kept after the session worked again")
	}
}

func TestARemoteSessionThatDoesNotClaimStallsWithItsScreen(t *testing.T) {
	w := onDevbox(t, behaviour{})
	w.execs[ref(2)].status = session.AgentIdle
	checks(t, w, int(ClaimWait/CheckEvery)+3)
	c := w.comments[ref(2)]
	if len(c) != 1 || !strings.Contains(c[0], "has not claimed") || !strings.Contains(c[0], "```text\nDo you want to proceed?\n```") {
		t.Errorf("comments %q", c)
	}
}

func TestADeadRemoteSessionFails(t *testing.T) {
	w := onDevbox(t, behaviour{claim: 1, dieAt: 2})
	checks(t, w, 3)
	if c := w.card(ref(2)); !c.Has(Failed) || c.Has(Running) || len(w.comments[ref(2)]) != 1 {
		t.Errorf("labels %v, comments %q", c.Labels, w.comments[ref(2)])
	}
}

func TestARemoteSessionThatDiesBeforeClaimingIsReleasedThereAndRestarted(t *testing.T) {
	w := onDevbox(t, behaviour{dieAt: 1})
	dead := w.execs[ref(2)].window.ID
	w.do[2] = behaviour{claim: 1}
	checks(t, w, 2)
	if !slices.Equal(w.killed, []string{dead}) || !slices.Equal(w.placed, []string{"2@devbox", "2@devbox"}) {
		t.Errorf("killed %v, placed %v\n%s", w.killed, w.placed, strings.Join(w.log, "\n"))
	}
	if c := w.comments[ref(2)]; len(c) != 1 || !strings.Contains(c[0], "ended before it claimed") {
		t.Errorf("comments %q", c)
	}
}

func TestAFinishedRemoteWindowIsReapedThroughRelease(t *testing.T) {
	w := onDevbox(t, behaviour{claim: 1, review: 2, merge: 3, linger: true, waitAt: 4})
	checks(t, w, 4+int(StallWait/CheckEvery)+1)
	if len(w.killed) != 1 || w.execs[ref(2)].window.ID != "" {
		t.Errorf("killed %v\n%s", w.killed, strings.Join(w.log, "\n"))
	}
}

func TestAnUnreachableHostLeavesItsSessionsAlone(t *testing.T) {
	w := onDevbox(t, behaviour{claim: 1, dieAt: 3})
	checks(t, w, 1)
	v := gather(t, w)
	w.hosts[1].down = "ssh: connect to host devbox: timed out"
	var due []bool
	down := outages{}
	for range 6 {
		w.sleep(CheckEvery)
		due = append(due, passDue(w, v, down))
		v = gather(t, w)
		down.note(w, v.Fleet)
		Apply(w, v, Decide(v))
	}
	c := w.card(ref(2))
	if c.Has(Failed) || !c.Has(Running) || len(w.comments[ref(2)]) != 0 || len(w.killed) != 0 || len(w.starts) != 1 {
		t.Errorf("labels %v, comments %q, killed %v, starts %v", c.Labels, w.comments[ref(2)], w.killed, w.starts)
	}
	if s := v.Sessions[ref(2)]; !s.Unknown || s.Host != "devbox" {
		t.Errorf("session %+v", s)
	}
	var lines []string
	for _, l := range w.log {
		if strings.HasPrefix(l, "host devbox") {
			lines = append(lines, l)
		}
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "its sessions are unknown") || !strings.Contains(lines[0], "timed out") {
		t.Errorf("log lines %q", lines)
	}
	// Only the first check, when the session became unknown, is due.
	if !slices.Equal(due, []bool{true, false, false, false, false, false}) {
		t.Errorf("due %v", due)
	}
	// Back again, it is seen dead, and failed.
	w.hosts[1].down = ""
	v = gather(t, w)
	down.note(w, v.Fleet)
	Apply(w, v, Decide(v))
	if !w.card(ref(2)).Has(Failed) || !slices.Contains(w.log, "host devbox answers again") {
		t.Errorf("labels %v\n%s", w.card(ref(2)).Labels, strings.Join(w.log, "\n"))
	}
}

func TestAnUnclaimedSessionOnAnUnreachableHostIsNotStartedAgain(t *testing.T) {
	w := onDevbox(t, behaviour{dieAt: 1})
	w.hosts[1].down = "ssh: connect to host devbox: timed out"
	w.hosts[0].caps = append(w.hosts[0].caps, "linux")
	checks(t, w, int(ClaimWait/CheckEvery)+2)
	if len(w.starts) != 1 || len(w.comments[ref(2)]) != 0 {
		t.Errorf("starts %v, comments %q\n%s", w.starts, w.comments[ref(2)], strings.Join(w.log, "\n"))
	}
}

func TestAnUnknownPlannerIsLeftAlone(t *testing.T) {
	w := newWorld(t, []string{"engine:feature", Approved})
	passOf(t, w)
	w.hosts = []fakeHost{{name: "mac", slots: 3, harnesses: []string{"claude"}, down: "tmux: no server"}}
	w.plans = nil
	checks(t, w, 3)
	if len(w.starts) != 1 || len(w.comments[ref(1)]) != 0 {
		t.Errorf("starts %v, comments %q", w.starts, w.comments[ref(1)])
	}
}

func TestClaudeAgentsFailingKeepsTheWait(t *testing.T) {
	w := onDevbox(t, behaviour{claim: 1, waitAt: 2})
	checks(t, w, 3)
	w.hosts[1].agentsDown = "claude agents: exit status 1"
	checks(t, w, 3)
	if !noteOf(t, w, 2, WaitingSince) {
		t.Error("an unknown wait cleared waiting-since")
	}
	if len(w.comments[ref(2)]) != 0 {
		t.Errorf("comments %q", w.comments[ref(2)])
	}
}

func TestAReleaseThatKeepsTheWorktreeIsLogged(t *testing.T) {
	w := newWorld(t, nil)
	w.keep = "worktree has uncommitted changes"
	reap := []Action{{Kind: Reap, Ref: ref(2), Session: Session{Name: "r-2", Host: "devbox"}}}
	Apply(w, View{}, reap)
	w.failNext = map[string]error{"release": os.ErrPermission}
	Apply(w, View{}, reap)
	want := []string{"release r-2 on devbox kept its worktree /wt/r-2: worktree has uncommitted changes", "reap " + ref(2).String() + " failed: permission denied"}
	for _, line := range want {
		if !slices.Contains(w.log, line) {
			t.Errorf("log lacks %q:\n%s", line, strings.Join(w.log, "\n"))
		}
	}
}

func TestARestartReleasesTheLastSessionEvenWithItsWindowGone(t *testing.T) {
	w := onDevbox(t, behaviour{})
	// The devbox rebooted: the window is gone, its worktree is not.
	w.execs[ref(2)].window = session.WindowInfo{}
	w.do[2] = behaviour{claim: 1}
	checks(t, w, 1)
	if !slices.Equal(w.released, []string{"r-2@devbox"}) || !slices.Equal(w.placed, []string{"2@devbox", "2@devbox"}) {
		t.Errorf("released %v, placed %v\n%s", w.released, w.placed, strings.Join(w.log, "\n"))
	}
}

func TestAStallIsFlaggedPerSessionWhateverTheClocks(t *testing.T) {
	c := card(2, "standard")
	// The last stall flagged a session started at 13:05 by one host's clock;
	// this one started at 13:01 by another's, which runs behind.
	s := Session{Name: "r-2", Host: "devbox", Spawned: t0.Add(time.Minute), StallNoted: t0.Add(5 * time.Minute)}
	v := View{Feature: tracker.Feature{Ref: ref(1), Open: true, Labels: []string{Executing}, Tasks: []tracker.Card{c}},
		Sessions: map[tracker.Ref]Session{ref(2): s}, Now: t0.Add(10 * time.Minute)}
	if acts := stalls(v); len(acts) != 1 || !acts[0].At.Equal(s.Spawned) {
		t.Errorf("a new session behind the clock: %+v", acts)
	}
	// Once flagged, the same session is not flagged again: the note keeps
	// whole seconds.
	s.Spawned = t0.Add(time.Minute + 300*time.Millisecond)
	s.StallNoted = t0.Add(time.Minute)
	v.Sessions[ref(2)] = s
	if acts := stalls(v); len(acts) != 0 {
		t.Errorf("flagged twice: %+v", acts)
	}
}
