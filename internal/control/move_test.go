package control

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// readFleet is a latest pass that read devbox and could not read mac.
func readFleet() HostStatus {
	return HostStatus{At: time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC),
		Hosts: []HostRow{{Name: "devbox"}, {Name: "mac", Unreachable: "ssh: exit 255"}, {Name: "spare"}}}
}

// movable is a service whose latest pass read the fleet, and a running
// task's records on it.
func movable(t *testing.T) (Service, Item, Actor, Attempt) {
	t.Helper()
	s := service(t)
	s.Hosts = readFleet
	task, worker, at := running(t, s)
	return s, task, worker, at
}

var coordinator = Actor{Name: "coordinator", Role: "coordinator"}

func TestMoveEndsTheAttemptMovedAndKeepsTheTask(t *testing.T) {
	s, task, worker, at := movable(t)
	call(t, s, worker, Request{Op: "state", ID: task.ID, Version: 2, State: "in-review", Item: Item{PR: good().PR}})
	call(t, s, worker, ready(task.ID, good()))
	before := len(mustSnapshot(t, s).Updates)
	req := Request{Op: "task-move", ID: task.ID, Key: "move-once", Move: &MoveRequest{Host: "spare"}}
	out := call(t, s, op, req)
	if out.Attempt.State != "ended" || out.Attempt.Reason != EndMoved || out.Attempt.MoveHost != "spare" || out.Attempt.MovedBlind {
		t.Fatalf("moved attempt %+v", out.Attempt)
	}
	if out.Item.State != "in-review" || out.Item.Held || out.Item.Version != 4 {
		t.Fatalf("moved task %+v", out.Item)
	}
	// The same key is a no-op: the first result, and no second update.
	if again := call(t, s, op, req); again.Item.Version != out.Item.Version || again.Attempt.Ended != out.Attempt.Ended {
		t.Fatalf("a repeat moved again: %+v", again)
	}
	st := mustSnapshot(t, s)
	if n := len(st.Updates) - before; n != 1 || !strings.HasPrefix(st.Updates[len(st.Updates)-1].Body, EndMoved+": ") {
		t.Fatalf("%d updates, last %+v", n, st.Updates[len(st.Updates)-1])
	}
	if !Vanished(st.LegacyTx(), st.Items[task.ID]) || st.Attempts[at.ID].Reason != EndMoved {
		t.Fatal("a moved task is not dispatched again as a resume")
	}
	if q := call(t, s, op, Request{Op: "merge-queue", ID: task.Repo}).Queue; len(q) != 1 || q[0].Task != task.ID {
		t.Fatalf("the move lost the queue place: %+v", q)
	}
	// The old session's credential is revoked.
	reject(t, s, worker, Request{Op: "say", ID: task.ID, Body: "still here"}, 403)
	// The next dispatch goes only where the move sends it, and stays in review.
	reject(t, s, op, Request{Op: "claim", ID: task.ID, Version: out.Item.Version, Body: "devbox", State: "claude"}, 409)
	next := call(t, s, op, Request{Op: "claim", ID: task.ID, Version: out.Item.Version, Body: "spare", State: "claude"})
	if next.Attempt.Host != "spare" || next.Item.State != "in-review" {
		t.Fatalf("the moved task's dispatch: %+v, %+v", next.Attempt, next.Item)
	}
}

func TestMoveWithNoHostAvoidsTheOldHostAndKeepsANamedHarness(t *testing.T) {
	s, task, _, _ := movable(t)
	out := call(t, s, op, Request{Op: "task-move", ID: task.ID, Move: &MoveRequest{Harness: "codex"}})
	if out.Attempt.MoveHost != "" || out.Attempt.MoveHarness != "codex" {
		t.Fatalf("moved attempt %+v", out.Attempt)
	}
	v := out.Item.Version
	reject(t, s, op, Request{Op: "claim", ID: task.ID, Version: v, Body: "devbox", State: "codex"}, 409)
	reject(t, s, op, Request{Op: "claim", ID: task.ID, Version: v, Body: "spare", State: "claude"}, 409)
	if next := call(t, s, op, Request{Op: "claim", ID: task.ID, Version: v, Body: "spare", State: "codex"}); next.Item.State != "running" {
		t.Fatalf("the moved task's dispatch: %+v", next.Item)
	}
}

// A move off a host the latest pass did not read is refused with its own
// reason until the operator confirms it; it is then blind.
func TestMoveOffAnUnreadHostNeedsConfirmation(t *testing.T) {
	for name, fleet := range map[string]func() HostStatus{
		"unreachable": func() HostStatus { f := readFleet(); f.Hosts[0].Unreachable = "ssh: exit 255"; return f },
		"no pass yet": nil,
		"not in it":   func() HostStatus { f := readFleet(); f.Hosts = f.Hosts[1:]; return f },
	} {
		t.Run(name, func(t *testing.T) {
			s := service(t)
			task, worker, at := running(t, s)
			s.Hosts = fleet
			_, err := s.Execute(op, Request{Op: "task-move", ID: task.ID, Key: "k", Move: &MoveRequest{}})
			var p *Problem
			if !errors.As(err, &p) || p.Status != 409 || p.Reason != ReasonMoveUnconfirmed || !strings.Contains(p.Message, "did not read host devbox") || !strings.Contains(p.Message, "may still be running and pushing") {
				t.Fatalf("err = %v", err)
			}
			if got := mustSnapshot(t, s).Attempts[at.ID]; got.State != "running" {
				t.Fatalf("a refused move ended the attempt: %+v", got)
			}
			call(t, s, worker, Request{Op: "say", ID: task.ID, Body: "still working"})
			out := call(t, s, op, Request{Op: "task-move", ID: task.ID, Key: "k", Move: &MoveRequest{Confirm: true}})
			if out.Attempt.Reason != EndMoved || !out.Attempt.MovedBlind {
				t.Fatalf("confirmed move %+v", out.Attempt)
			}
		})
	}
}

// Moving a task again before its next dispatch changes where it goes, and
// ends nothing more.
func TestMovingAMovedTaskChangesItsDestination(t *testing.T) {
	s, task, _, at := movable(t)
	first := call(t, s, op, Request{Op: "task-move", ID: task.ID, Move: &MoveRequest{Host: "spare"}})
	second := call(t, s, op, Request{Op: "task-move", ID: task.ID, Version: first.Item.Version, Move: &MoveRequest{Host: "devbox", Harness: "codex"}})
	if second.Attempt.ID != at.ID || !second.Attempt.Ended.Equal(first.Attempt.Ended) || second.Attempt.MoveHost != "devbox" || second.Attempt.MoveHarness != "codex" {
		t.Fatalf("second move %+v", second.Attempt)
	}
	st := mustSnapshot(t, s)
	if last := st.Updates[len(st.Updates)-1]; last.Kind != "moved" {
		t.Fatalf("a second move posted %+v", last)
	}
	call(t, s, op, Request{Op: "claim", ID: task.ID, Version: second.Item.Version, Body: "devbox", State: "codex"})
}

// A move confirmed at a terminal, repeated under its key without the
// confirmation, or the other way round, returns the first result.
func TestARepeatedMoveIgnoresItsConfirmation(t *testing.T) {
	s, task, _, _ := movable(t)
	first := call(t, s, op, Request{Op: "task-move", ID: task.ID, Key: "k", Move: &MoveRequest{Host: "spare", Confirm: true}})
	again := call(t, s, op, Request{Op: "task-move", ID: task.ID, Key: "k", Move: &MoveRequest{Host: "spare"}})
	if again.Item.Version != first.Item.Version || !again.Attempt.Ended.Equal(first.Attempt.Ended) {
		t.Fatalf("the repeat moved again: %+v", again.Item)
	}
	reject(t, s, op, Request{Op: "task-move", ID: task.ID, Key: "k", Move: &MoveRequest{Host: "mac"}}, 409)
}

// A host whose run records could not be read is unread, so a move off it
// needs confirming; once the old window was released, a move again does not.
func TestMoveConfirmationFollowsWhatTheOldHostCanStillDo(t *testing.T) {
	s := service(t)
	task, _, at := running(t, s)
	s.Hosts = func() HostStatus { f := readFleet(); f.Hosts[0].RunsError = "permission denied"; return f }
	reject(t, s, op, Request{Op: "task-move", ID: task.ID, Move: &MoveRequest{}}, 409)
	call(t, s, op, Request{Op: "task-move", ID: task.ID, Move: &MoveRequest{Confirm: true}})
	if err := s.MovedReleased(at.ID); err != nil {
		t.Fatal(err)
	}
	if out := call(t, s, op, Request{Op: "task-move", ID: task.ID, Move: &MoveRequest{Host: "spare"}}); out.Attempt.MovedBlind {
		t.Fatalf("a released move is blind: %+v", out.Attempt)
	}
}

func TestMovedReleasedMarksOnlyAMovedAttempt(t *testing.T) {
	s, task, _, at := movable(t)
	if err := s.MovedReleased(at.ID); err == nil {
		t.Fatal("a running attempt was marked released")
	}
	call(t, s, op, Request{Op: "task-move", ID: task.ID, Move: &MoveRequest{}})
	for range 2 {
		if err := s.MovedReleased(at.ID); err != nil {
			t.Fatal(err)
		}
	}
	if got := mustSnapshot(t, s).Attempts[at.ID]; !got.MoveReleased || got.Reason != EndMoved {
		t.Fatalf("marked attempt %+v", got)
	}
}

func TestMoveRefusesWhatItCannotMove(t *testing.T) {
	s := service(t)
	s.Hosts = readFleet
	repo, plan := repoPlan(t, s)
	idle := newTask(t, s, repo.ID, plan.ID)
	approve(t, s, plan)
	move := func(id string, m *MoveRequest) Request { return Request{Op: "task-move", ID: id, Move: m} }
	reject(t, s, op, move(idle.ID, &MoveRequest{}), 409)
	reject(t, s, op, move(plan.ID, &MoveRequest{}), 404)
	worker, out := dispatch(t, s, idle)
	reject(t, s, op, move(idle.ID, &MoveRequest{}), 409) // the dispatch is in flight
	launched(t, s, out.Attempt.ID)
	for _, a := range []Actor{worker, coordinator} {
		reject(t, s, a, move(idle.ID, &MoveRequest{}), 403)
	}
	reject(t, s, op, move(idle.ID, nil), 400)
	reject(t, s, op, move(idle.ID, &MoveRequest{Host: "elsewhere"}), 400)
	reject(t, s, op, Request{Op: "task-move", ID: idle.ID, Version: 1, Move: &MoveRequest{}}, 409)
	reject(t, s, op, Request{Op: "task-stop", ID: idle.ID, Move: &MoveRequest{}}, 400)
	call(t, s, op, Request{Op: "task-stop", ID: idle.ID})
	reject(t, s, op, move(idle.ID, &MoveRequest{}), 409) // held, its attempt stopped
}
