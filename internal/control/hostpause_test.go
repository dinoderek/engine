package control

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

// onHosts is a service whose latest pass read devbox and spare, with plan
// tasks running on them: t1 claude on devbox, t2 codex on devbox, t3 claude
// on spare.
func onHosts(t *testing.T) (Service, []Item) {
	t.Helper()
	s := service(t)
	s.Hosts = func() HostStatus {
		return HostStatus{At: time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC), Hosts: []HostRow{{Name: "devbox"}, {Name: "mac", Unreachable: "ssh: exit 255"}, {Name: "spare"}}}
	}
	repo, plan := repoPlan(t, s)
	var tasks []Item
	for range 3 {
		tasks = append(tasks, newTask(t, s, repo.ID, plan.ID))
	}
	approve(t, s, plan)
	for i, where := range [][2]string{{"devbox", "claude"}, {"devbox", "codex"}, {"spare", "claude"}} {
		out := call(t, s, op, Request{Op: "claim", ID: tasks[i].ID, Body: where[0], State: where[1]})
		launched(t, s, out.Attempt.ID)
	}
	return s, tasks
}

func pause(t *testing.T, s Service, verb, host string, key string) HostChange {
	t.Helper()
	return *call(t, s, op, Request{Op: "host-" + verb, ID: host, Key: key}).Host
}

func TestPauseAndResumeAreIdempotentUnderOneKeyAndWithout(t *testing.T) {
	s, _ := onHosts(t)
	first := pause(t, s, "pause", "devbox", "p1")
	if !first.Paused || first.PausedAt.IsZero() || first.Name != "devbox" {
		t.Fatalf("pause: %+v", first)
	}
	updates := len(mustSnapshot(t, s).Updates)
	// The same key returns the first result, and so does a fresh key: the
	// pause keeps the time it was first set.
	s.Now = func() time.Time { return first.PausedAt.Add(time.Hour) }
	if again := pause(t, s, "pause", "devbox", "p1"); !again.PausedAt.Equal(first.PausedAt) {
		t.Fatalf("same key: %+v", again)
	}
	if again := pause(t, s, "pause", "devbox", "p2"); !again.Paused || !again.PausedAt.Equal(first.PausedAt) {
		t.Fatalf("new key: %+v", again)
	}
	if !mustSnapshot(t, s).Hosts["devbox"].Paused || len(mustSnapshot(t, s).Updates) != updates {
		t.Fatal("a repeated pause changed the record or the history")
	}
	reject(t, s, op, Request{Op: "host-pause", ID: "devbox", Key: "p1", Version: 3}, 409) // the key's command differs
	resumed := pause(t, s, "resume", "devbox", "r1")
	if resumed.Paused || !resumed.PausedAt.IsZero() || mustSnapshot(t, s).Hosts["devbox"].Paused {
		t.Fatalf("resume: %+v", resumed)
	}
	if again := pause(t, s, "resume", "devbox", "r2"); again.Paused {
		t.Fatalf("a second resume: %+v", again)
	}
	if got := pause(t, s, "resume", "spare", "r3"); got.Paused {
		t.Fatalf("resuming a host never paused: %+v", got)
	}
}

func TestAPauseSurvivesTheHostsTmuxBeingRecorded(t *testing.T) {
	s, _ := onHosts(t)
	pause(t, s, "pause", "devbox", "p")
	if _, err := s.SawTmux("devbox", TmuxRead{Known: true, Start: 77}, func(*Tx, Attempt) bool { return true }); err != nil {
		t.Fatal(err)
	}
	if rec := mustSnapshot(t, s).Hosts["devbox"]; !rec.Paused || rec.TmuxStart != 77 {
		t.Fatalf("host record %+v", rec)
	}
}

func TestAPausedHostRefusesADispatchButOthersTakeOne(t *testing.T) {
	s, tasks := onHosts(t)
	pause(t, s, "pause", "devbox", "p")
	stopped := func() {
		call(t, s, op, Request{Op: "task-stop", ID: tasks[0].ID})
		call(t, s, op, Request{Op: "task-resume", ID: tasks[0].ID})
	}
	stopped()
	reject(t, s, op, Request{Op: "claim", ID: tasks[0].ID, Body: "devbox", State: "claude"}, 409)
	launched(t, s, call(t, s, op, Request{Op: "claim", ID: tasks[0].ID, Body: "spare", State: "claude"}).Attempt.ID)
	pause(t, s, "resume", "devbox", "r")
	stopped()
	call(t, s, op, Request{Op: "claim", ID: tasks[0].ID, Body: "devbox", State: "claude"})
}

func TestPausedRunningSessionsAreUntouched(t *testing.T) {
	s, tasks := onHosts(t)
	pause(t, s, "pause", "devbox", "p")
	st := mustSnapshot(t, s)
	for _, task := range tasks {
		if got := st.Attempts[st.Items[task.ID].Attempt]; got.State != "running" || st.Items[task.ID].State != "running" {
			t.Fatalf("pausing touched %s: %+v", task.ID, got)
		}
	}
}

func TestHostMoveMovesEveryTaskThereOrOnlyOneHarnesss(t *testing.T) {
	for name, c := range map[string]struct {
		harness string
		moved   []int // indexes of the tasks on devbox
	}{
		"every task":  {"", []int{0, 1}},
		"one harness": {"codex", []int{1}},
	} {
		t.Run(name, func(t *testing.T) {
			s, tasks := onHosts(t)
			var want []string
			for _, i := range c.moved {
				want = append(want, tasks[i].ID)
			}
			req := Request{Op: "host-move", ID: "devbox", Key: "k", Move: &MoveRequest{Harness: c.harness}}
			out := call(t, s, op, req)
			if !out.Host.Paused || out.Host.Name != "devbox" || len(out.Host.Left) != 0 || !slices.Equal(out.Host.Moved, want) {
				t.Fatalf("host move: %+v, want moved %v", out.Host, want)
			}
			st := mustSnapshot(t, s)
			if !st.Hosts["devbox"].Paused {
				t.Error("the host was not paused")
			}
			for _, task := range tasks {
				checkMoved(t, st, task.ID, slices.Contains(want, task.ID))
			}
			// A repeat under its key returns the first result; under a new
			// key it finds nothing left to move, and adds no history.
			if again := call(t, s, op, req); !slices.Equal(again.Host.Moved, want) {
				t.Errorf("same key: %+v", again.Host)
			}
			updates := len(mustSnapshot(t, s).Updates)
			req.Key = "k2"
			if again := call(t, s, op, req); !again.Host.Paused || len(again.Host.Moved) != 0 || len(mustSnapshot(t, s).Updates) != updates {
				t.Errorf("a repeat moved again: %+v", again.Host)
			}
		})
	}
}

// checkMoved fails unless task's attempt ended moved, to anywhere, exactly
// when it should have.
func checkMoved(t *testing.T, st state, task string, moved bool) {
	t.Helper()
	at := st.Attempts[st.Items[task].Attempt]
	if (at.Reason == EndMoved) != moved || moved && (at.MoveHost != "" || at.MoveHarness != "" || at.MovedBlind) {
		t.Errorf("%s: attempt %+v, moved %v", task, at, moved)
	}
}

// A moved task resumes, and not back where it came from: the host is paused
// and the move sends it anywhere else.
func TestHostMoveSendsTasksElsewhereAndLeavesADispatchInFlight(t *testing.T) {
	s, tasks := onHosts(t)
	repo := tasks[0].Repo
	plan := tasks[0].Plan
	extra := newTask(t, s, repo, plan)
	call(t, s, op, Request{Op: "claim", ID: extra.ID, Body: "devbox", State: "claude"}) // its dispatch is in flight
	out := call(t, s, op, Request{Op: "host-move", ID: "devbox", Move: &MoveRequest{}})
	if !slices.Equal(out.Host.Moved, []string{tasks[0].ID, tasks[1].ID}) || !slices.Equal(out.Host.Left, []string{extra.ID}) {
		t.Fatalf("moved %v, left %v", out.Host.Moved, out.Host.Left)
	}
	reject(t, s, op, Request{Op: "claim", ID: tasks[0].ID, Body: "devbox", State: "claude"}, 409)
	if next := call(t, s, op, Request{Op: "claim", ID: tasks[0].ID, Body: "spare", State: "claude"}); next.Attempt.Host != "spare" {
		t.Fatalf("the moved task's dispatch: %+v", next.Attempt)
	}
}

// An unreadable host asks once for the whole host, and refuses all of it:
// nothing is paused or moved until the operator confirms.
func TestHostMoveOffAnUnreadHostConfirmsOnceForTheWholeHost(t *testing.T) {
	s, tasks := onHosts(t)
	s.Hosts = func() HostStatus {
		f := readFleet()
		f.Hosts[0].Unreachable = "ssh: exit 255"
		return f
	}
	req := Request{Op: "host-move", ID: "devbox", Key: "k", Move: &MoveRequest{}}
	_, err := s.Execute(op, req)
	var p *Problem
	if !errors.As(err, &p) || p.Status != 409 || p.Reason != ReasonMoveUnconfirmed || !strings.Contains(p.Message, "did not read host devbox") ||
		!strings.Contains(p.Message, "its 2 tasks") {
		t.Fatalf("err = %v", err)
	}
	st := mustSnapshot(t, s)
	if st.Hosts["devbox"].Paused || st.Attempts[st.Items[tasks[0].ID].Attempt].State != "running" {
		t.Fatal("a refused host move paused the host or ended an attempt")
	}
	req.Move.Confirm = true
	out := call(t, s, op, req)
	st = mustSnapshot(t, s)
	if len(out.Host.Moved) != 2 || !st.Hosts["devbox"].Paused || !st.Attempts[st.Items[tasks[0].ID].Attempt].MovedBlind || !st.Attempts[st.Items[tasks[1].ID].Attempt].MovedBlind {
		t.Fatalf("confirmed host move: %+v", out.Host)
	}
	// Repeating it under its key, unconfirmed, returns the first result.
	req.Move = &MoveRequest{}
	if again := call(t, s, op, req); len(again.Host.Moved) != 2 {
		t.Fatalf("repeat: %+v", again.Host)
	}
	// A host with nothing running on it needs no confirmation.
	s2, _ := onHosts(t)
	s2.Hosts = s.Hosts
	if out := call(t, s2, op, Request{Op: "host-move", ID: "mac", Move: &MoveRequest{}}); len(out.Host.Moved) != 0 || !out.Host.Paused {
		t.Fatalf("an empty unreadable host: %+v", out.Host)
	}
}

func TestHostOpsRefuseWhatTheyCannotDo(t *testing.T) {
	s, tasks := onHosts(t)
	worker := func() Actor {
		st := mustSnapshot(t, s)
		return Actor{Name: st.Items[tasks[0].ID].Attempt, Role: "worker", Attempt: st.Items[tasks[0].ID].Attempt}
	}()
	for _, verb := range []string{"pause", "resume", "move"} {
		req := Request{Op: "host-" + verb, ID: "devbox", Move: &MoveRequest{}}
		if verb != "move" {
			req.Move = nil
		}
		for _, a := range []Actor{worker, coordinator} {
			reject(t, s, a, req, 403)
		}
		req.ID = ""
		reject(t, s, op, req, 400)
		req.ID = "elsewhere"
		if verb != "resume" {
			reject(t, s, op, req, 400)
		}
	}
	reject(t, s, op, Request{Op: "host-move", ID: "devbox"}, 400)
	reject(t, s, op, Request{Op: "host-move", ID: "devbox", Move: &MoveRequest{Host: "spare"}}, 400)
	reject(t, s, op, Request{Op: "host-pause", ID: "devbox", Move: &MoveRequest{}}, 400)
	// A host removed from hosts.toml can still have its pause lifted.
	call(t, s, op, Request{Op: "host-pause", ID: "devbox"})
	s.Hosts = func() HostStatus { return HostStatus{At: time.Now(), Hosts: []HostRow{{Name: "spare"}}} }
	if got := pause(t, s, "resume", "devbox", "gone"); got.Paused {
		t.Fatalf("resume of a removed host: %+v", got)
	}
	reject(t, s, op, Request{Op: "host-pause", ID: "devbox"}, 400)
	// Before any pass the server knows no hosts, and takes the name.
	s.Hosts = nil
	call(t, s, op, Request{Op: "host-pause", ID: "anywhere"})
}
