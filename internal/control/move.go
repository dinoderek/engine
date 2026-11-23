package control

import (
	"fmt"
	"slices"
)

// moveTask is the operator's move of a task's execution to another host or
// harness, in one transaction: the running attempt ends EndMoved, which
// revokes its credential, and records where the next dispatch goes. The
// task keeps its state and merge queue place, and is dispatched again as a
// resume (Vanished). The coordinator releases the old window once its host
// answers, pushing the branch's commits first. Moving a task whose attempt
// already ended moved, before its next dispatch, changes where it goes.
//
// A host the latest coordinator pass did not read may still run the old
// session, which can push though it can no longer write here: the move is
// refused with ReasonMoveUnconfirmed unless the request confirms it, and is
// then recorded as blind, so the next dispatch need not wait for that host.
func (s Service) moveTask(st *State, a Actor, r Request) (Response, error) {
	item, err := heldTarget(st, a, r)
	if err != nil {
		return Response{}, err
	}
	if r.Move == nil {
		return Response{}, problem(400, "a move names its destination, even if empty")
	}
	at, ok := st.Attempts[item.Attempt]
	switch {
	case !ok || item.State == "done" || item.State == "failed" || item.Held || at.State == "ended" && at.Reason != EndMoved:
		return Response{}, problem(409, "task %s has no running execution to move", item.ID)
	case at.State == "dispatching":
		return Response{}, problem(409, "task %s's dispatch is still in flight; move it once its session has started", item.ID)
	}
	fleet := s.fleet()
	if r.Move.Host != "" && !fleet.At.IsZero() && !slices.ContainsFunc(fleet.Hosts, func(h HostRow) bool { return h.Name == r.Move.Host }) {
		return Response{}, problem(400, "host %s is not in hosts.toml", r.Move.Host)
	}
	// A released window has no session left to warn about.
	read := fleet.read(at.Host) || at.MoveReleased
	if !read && !r.Move.Confirm {
		return Response{}, &Problem{Status: 409, Reason: ReasonMoveUnconfirmed, Message: fmt.Sprintf(
			"the coordinator's latest pass did not read host %s: task %s's session there may still be running and pushing to its branch, "+
				"though its credential is revoked; confirm to move it anyway", at.Host, item.ID)}
	}
	kind := "moved"
	if at.State != "ended" {
		at.State, at.Ended, at.Reason = "ended", s.now(), EndMoved
		kind = "execution-ended"
	}
	at.MoveHost, at.MoveHarness, at.MovedBlind = r.Move.Host, r.Move.Harness, !read
	st.Attempts[at.ID] = at
	// The version moves so that a pass placed on the old record dispatches
	// nothing.
	item.Version++
	st.Items[item.ID] = item
	st.update(a, r, kind, EndMoved+": the operator moved the task; "+moveText(at), s.now())
	return Response{Item: &item, Attempt: &at}, nil
}

// MovedReleased records that the coordinator released moved attempt id's
// window, so no pass looks for it again. It is idempotent.
func (s Service) MovedReleased(id string) error {
	return s.Store.Transaction(func(st *State) error {
		at, ok := st.Attempts[id]
		if !ok || at.Reason != EndMoved {
			return problem(409, "execution %s did not end moved", id)
		}
		if !at.MoveReleased {
			at.MoveReleased = true
			st.Attempts[id] = at
		}
		return nil
	})
}

// digested is r as its request key's receipt compares it: a move's
// confirmation is left out, so repeating a confirmed move under its key,
// confirmed again or not, returns the first result.
func digested(r Request) Request {
	if r.Move != nil && r.Move.Confirm {
		m := *r.Move
		m.Confirm = false
		r.Move = &m
	}
	return r
}

// fleet is the coordinator's latest pass over the hosts, or none.
func (s Service) fleet() HostStatus {
	if s.Hosts == nil {
		return HostStatus{}
	}
	return s.Hosts()
}

// read reports whether the latest pass read host, its run records included.
func (f HostStatus) read(host string) bool {
	i := slices.IndexFunc(f.Hosts, func(h HostRow) bool { return h.Name == host })
	return !f.At.IsZero() && i >= 0 && f.Hosts[i].Unreachable == "" && f.Hosts[i].RunsError == ""
}

// moveText says where moved attempt at sends its task, and what happens to
// the old session.
func moveText(at Attempt) string {
	where := "on any host but " + at.Host
	if at.MoveHost != "" {
		where = "on host " + at.MoveHost
	}
	if at.MoveHarness != "" {
		where += " with harness " + at.MoveHarness
	}
	text := "it is dispatched again as a resume " + where + ", once its session on " + at.Host + " is released, pushing its commits first"
	if at.MovedBlind {
		text = "it is dispatched again as a resume " + where + " without waiting for " + at.Host +
			", which could not be read: its old session there may still push. Its window is released when the host answers"
	}
	return text
}

// moveDestination refuses a dispatch of a task whose current attempt ended
// moved anywhere its move does not send it.
func moveDestination(st *State, item Item, host, harness string) error {
	at, ok := st.Attempts[item.Attempt]
	if !ok || at.Reason != EndMoved {
		return nil
	}
	switch {
	case at.MoveHost != "" && host != at.MoveHost:
		return problem(409, "task %s was moved to host %s, not %s", item.ID, at.MoveHost, host)
	case at.MoveHost == "" && host == at.Host:
		return problem(409, "task %s was moved off host %s", item.ID, host)
	case at.MoveHarness != "" && harness != at.MoveHarness:
		return problem(409, "task %s was moved to harness %s, not %s", item.ID, at.MoveHarness, harness)
	}
	return nil
}
