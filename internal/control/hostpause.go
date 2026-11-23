package control

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// HostChange is what a host-pause, host-resume or host-move did to one host:
// whether it is paused now and since when, and for a move the tasks it moved
// and those it could not.
type HostChange struct {
	Name     string    `json:"name"`
	Paused   bool      `json:"paused"`
	PausedAt time.Time `json:"paused_at,omitzero"`
	// Moved are the tasks moved off the host, in task order. Left are the
	// ones whose dispatch was still in flight: they have no session to
	// move yet, and a later host move takes them.
	Moved []string `json:"moved,omitempty"`
	Left  []string `json:"left,omitempty"`
}

// pauseHost is the operator's pause or resume of a host, in one transaction.
// A paused host takes no new session: placement skips it, and a dispatch of
// a pass that read the record earlier is refused (reserve). Sessions already
// running there carry on. Pausing a paused host, or resuming one that is not,
// changes nothing.
func (s Service) pauseHost(tx *Tx, a Actor, r Request) (Response, error) {
	resume := r.Op == "host-resume"
	if err := s.hostTarget(tx, a, r, resume); err != nil {
		return Response{}, err
	}
	change := s.setPaused(tx, r.ID, !resume)
	return Response{Host: &change}, nil
}

// hostTarget checks that an operator names a host hosts.toml has. A resume
// also takes a paused host the file no longer has, so its pause can be lifted.
func (s Service) hostTarget(tx *Tx, a Actor, r Request, resume bool) error {
	if err := operator(a); err != nil {
		return err
	}
	if r.ID == "" {
		return problem(400, "%s names a host", r.Op)
	}
	if resume && tx.Host(r.ID).Paused {
		return nil
	}
	return knownHost(s.fleet(), r.ID)
}

// setPaused sets host's pause, keeping the time of one already set.
func (s Service) setPaused(tx *Tx, host string, paused bool) HostChange {
	rec := tx.Host(host)
	if rec.Paused != paused {
		rec.Paused, rec.PausedAt = paused, time.Time{}
		if paused {
			rec.PausedAt = s.now()
		}
		tx.PutHost(host, rec)
	}
	return HostChange{Name: host, Paused: rec.Paused, PausedAt: rec.PausedAt}
}

// dispatchTarget refuses a dispatch of item to a paused host, or anywhere its
// move does not send it.
func dispatchTarget(tx *Tx, item Item, host, harness string) error {
	if tx.Host(host).Paused {
		return problem(409, "host %s is paused", host)
	}
	return moveDestination(tx, item, host, harness)
}

// moveHost is the operator's evacuation of a host, in one transaction: the
// host is paused, and every task running there, or only those on
// move.Harness, is moved off it as taskMove does, to any other host and the
// task's own choice of harness. A host the latest pass did not read may still
// run those sessions: the whole move is refused with ReasonMoveUnconfirmed
// until the request confirms it, once for the host, and each task is then
// moved blind. Tasks already moved are not moved again, so a repeat moves only
// what started since.
func (s Service) moveHost(tx *Tx, a Actor, r Request) (Response, error) {
	if err := s.hostTarget(tx, a, r, false); err != nil {
		return Response{}, err
	}
	if r.Move == nil || r.Move.Host != "" {
		return Response{}, problem(400, "a host move names its harness filter, even if empty, and no destination")
	}
	tasks := hostTasks(tx, r.ID, r.Move.Harness)
	if len(tasks) > 0 && !r.Move.Confirm && !s.fleet().read(r.ID) {
		return Response{}, &Problem{Status: 409, Reason: ReasonMoveUnconfirmed, Message: fmt.Sprintf(
			"the coordinator's latest pass did not read host %s: the sessions of its %d tasks (%s) may still be running and pushing to their branches, "+
				"though their credentials are revoked; confirm to move them anyway", r.ID, len(tasks), strings.Join(tasks, ", "))}
	}
	change := s.setPaused(tx, r.ID, true)
	for _, id := range tasks {
		_, err := s.moveTask(tx, a, Request{Op: "task-move", ID: id, Move: &MoveRequest{Confirm: true}})
		var p *Problem
		switch {
		case err == nil:
			change.Moved = append(change.Moved, id)
		case errors.As(err, &p) && p.Status == 409:
			change.Left = append(change.Left, id)
		default:
			return Response{}, err
		}
	}
	return Response{Host: &change}, nil
}

// hostTasks lists, in task order, the tasks whose current attempt is a live
// or starting session on host, only those of harness when it is set.
func hostTasks(tx *Tx, host, harness string) []string {
	var out []string
	for _, at := range tx.AttemptsOnHost(host) {
		item := tx.item(at.Item)
		if at.State != "ended" && item.Kind == "task" && item.Attempt == at.ID && (harness == "" || at.Harness == harness) {
			out = append(out, item.ID)
		}
	}
	slices.SortFunc(out, func(a, b string) int { return Number(a) - Number(b) })
	return out
}
