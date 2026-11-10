package reconcile

import (
	"fmt"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hostops"
)

// The merge queue's bounds. Each wait is fixed here, so no request waits
// silently and none holds its repository's queue for longer. A head that
// outstays one is refused, and its executor requests the merge again.
const (
	// TurnWait is how long a head told to refresh has to request again.
	TurnWait = 45 * time.Minute
	// ChecksWait is how long a head's checks may stay pending, from when it
	// became head or requested its head commit, whichever is later.
	ChecksWait = 60 * time.Minute
	// MergeableWait is how long GitHub may not know if the head mergeable.
	MergeableWait = 10 * time.Minute
	// HostWait is how long the head's host may stay unreachable.
	HostWait = 15 * time.Minute
	// GhFailures is how many failed merge calls in a row refuse the head.
	GhFailures = 3
	// CheckEvery is how often the head's host call runs. A new head, or a
	// head with a new request, is called at once.
	CheckEvery = 2 * time.Minute
)

// QueueView is one repository's merge queue as a pass decides on it.
type QueueView struct {
	Now time.Time
	// Entries are in queue order.
	Entries []QueueItem
}

// QueueItem is one entry with the records it is decided on.
type QueueItem struct {
	control.QueueEntry
	// Request is the entry's newest merge request.
	Request control.Update
	// State and Question are the task's state and unresolved question.
	State    string
	Question int
	// Stopped is true when the task's coordination, or its plan's, is stopped.
	Stopped bool
	// Host is where the task's latest attempt ran: its PR merges there.
	Host string
}

// eligible reports whether the entry may be the head.
func (q QueueItem) eligible() bool {
	return q.State == "in-review" && q.Question == 0 && !q.Stopped
}

// Probe is what the head's host call said of its PR.
type Probe struct {
	Reply hostops.Merged
	// Unreachable is true when the host could not be reached: nothing about
	// the PR is known.
	Unreachable bool
	// Err is the failed call's error.
	Err string
	// Missing is true when the task's host is not in the host config: no
	// call was made.
	Missing bool
}

// DecideQueue is one pass over a repository's queue. It keeps the head, or
// picks the first eligible entry, and returns the steps that change entries
// first; with none, the head's merge call when one is due. Waiting entries
// are never called. A head that stops being eligible gives up the head and
// keeps its place.
func DecideQueue(v QueueView) []Action {
	head := -1
	for i, q := range v.Entries {
		if q.Phase != control.QueueWaiting {
			head = i
		}
	}
	if head >= 0 && !v.Entries[head].eligible() {
		q := v.Entries[head]
		to := unheaded(q.QueueEntry)
		return []Action{step(q, control.QueueStep{To: &to})}
	}
	if head < 0 {
		for _, q := range v.Entries {
			if q.eligible() {
				to := q.QueueEntry
				to.Phase, to.HeadAt = control.QueueCheck, v.Now
				return []Action{step(q, control.QueueStep{To: &to})}
			}
		}
		return nil
	}
	q := v.Entries[head]
	switch {
	case q.Phase == control.QueueRefresh && v.Now.Sub(q.TurnAt) >= TurnWait:
		return []Action{giveUp(q, "it was not refreshed within "+TurnWait.String()+" of its turn",
			"Merge the default branch in, rerun the gates and the fresh review, push")}
	case q.Phase == control.QueueCheck && (q.CheckedAt.IsZero() || v.Now.Sub(q.CheckedAt) >= CheckEvery):
		return []Action{{Kind: Merge, Item: q.Task, Host: q.Host, Entry: q.QueueEntry, Request: q.Request}}
	}
	return nil
}

func step(q QueueItem, s control.QueueStep) Action {
	s.From = q.QueueEntry
	return Action{Kind: QueueStep, Item: q.Task, Step: &s}
}

// unheaded is e back in the queue, no longer the head.
func unheaded(e control.QueueEntry) control.QueueEntry {
	e.Phase = control.QueueWaiting
	e.HeadAt, e.TurnAt, e.CheckedAt, e.WaitSince, e.FailSince, e.Failures = time.Time{}, time.Time{}, time.Time{}, time.Time{}, time.Time{}, 0
	return e
}

// giveUp ends q's entry with a merge-refused answer: the executor is told
// why and what to do, then requests the merge again; the operator is told
// why.
func giveUp(q QueueItem, why, fix string) Action {
	return step(q, control.QueueStep{Kind: control.MergeRefused,
		Text:   "Not merged: " + why + ". " + fix + ", then request the merge again.",
		Notice: fmt.Sprintf("%s left the merge queue: %s.", q.PR, why)})
}

// Answer is the step that what the head's host call said of its PR calls
// for, if any.
func Answer(now time.Time, q QueueItem, p Probe) []Action {
	to := q.QueueEntry
	to.CheckedAt, to.FailSince, to.Failures = now, time.Time{}, 0
	switch {
	case p.Missing:
		return []Action{giveUp(q, fmt.Sprintf("its task's host %q is not in hosts.toml", q.Host),
			"Ask the operator to fix the host config with engine ask")}
	case p.Unreachable:
		to.FailSince, to.Failures = q.FailSince, q.Failures
		if q.FailSince.IsZero() {
			to.FailSince = now
		} else if now.Sub(q.FailSince) >= HostWait {
			return []Action{giveUp(q, fmt.Sprintf("host %s was unreachable for %s", q.Host, HostWait),
				"Ask the operator to bring the host back with engine ask")}
		}
		return []Action{step(q, control.QueueStep{To: &to})}
	case p.Err != "":
		to.FailSince, to.Failures = q.FailSince, q.Failures+1
		if to.Failures >= GhFailures {
			return []Action{giveUp(q, fmt.Sprintf("merging at %s failed %d times on host %s: %s", q.Head, GhFailures, q.Host, p.Err),
				"Ask the operator to check gh auth status and engine setup there with engine ask")}
		}
		return []Action{step(q, control.QueueStep{To: &to})}
	}
	return answerHead(now, q, to, p.Reply)
}

// answerHead is the step for what the head's merge call reported. to is the
// head with its failure counts cleared.
func answerHead(now time.Time, q QueueItem, to control.QueueEntry, r hostops.Merged) []Action {
	healthy := func() []Action { return []Action{step(q, control.QueueStep{To: &to})} }
	switch r.Code {
	case hostops.CodeMerged, hostops.CodeMergedElsewhere:
		return []Action{merged(q, r)}
	case hostops.CodePendingChecks, "":
		// An empty code is a reply the queue cannot read: it waits no longer
		// than pending checks do.
		since := q.HeadAt
		if q.Request.At.After(since) {
			since = q.Request.At
		}
		if now.Sub(since) >= ChecksWait {
			return []Action{giveUp(q, fmt.Sprintf("its checks were pending for %s (%s)", ChecksWait, r.Reason),
				"Find out why the checks do not finish, fix it, push")}
		}
		return healthy()
	case hostops.CodePendingMergeable:
		if q.WaitSince.IsZero() {
			to.WaitSince = now
		} else if now.Sub(q.WaitSince) >= MergeableWait {
			return []Action{giveUp(q, "GitHub did not work out whether it is mergeable within "+MergeableWait.String(),
				"Merge the default branch in, rerun the gates and the fresh review, push")}
		}
		return healthy()
	case hostops.CodeBehind, hostops.CodeConflict, hostops.CodeHeadMoved:
		to.Phase, to.TurnAt, to.WaitSince, to.Reason = control.QueueRefresh, now, time.Time{}, r.Reason
		return []Action{step(q, control.QueueStep{To: &to, Kind: control.MergeTurn, Text: fmt.Sprintf(
			"Your turn in the merge queue: %s. Merge the default branch in, rerun the gates and the fresh review, push, then run engine task merge-ready for the new head within %s.",
			r.Reason, TurnWait)})}
	}
	return []Action{refused(q, r)}
}

// merged completes q's task. A PR merged at a head other than the reviewed
// one is done too, and the operator is told.
func merged(q QueueItem, r hostops.Merged) Action {
	method := ""
	if q.Request.Merge != nil {
		method = " (" + q.Request.Merge.Method + ")"
	}
	s := control.QueueStep{Done: true, Kind: control.MergeMerged, Text: fmt.Sprintf("Merged %s%s: %s.", q.PR, method, r.Reason)}
	if r.Code == hostops.CodeMergedElsewhere {
		s.Notice = fmt.Sprintf("%s merged outside engine: %s. The task is done; check the merged commit.", q.PR, r.Reason)
	}
	return step(q, s)
}

// refused ends q's entry: the executor fixes the PR and requests again. A
// closed or blocked PR is the operator's to know of too.
func refused(q QueueItem, r hostops.Merged) Action {
	s := control.QueueStep{Kind: control.MergeRefused, Text: "Not merged: " + r.Reason + ". Fix it and request the merge again."}
	switch r.Code {
	case hostops.CodeBlocked:
		s.Text = "Not merged: " + r.Reason + ". Ask the operator with engine ask, then request the merge again."
		s.Notice = fmt.Sprintf("%s left the merge queue: %s.", q.PR, r.Reason)
	case hostops.CodeClosed:
		s.Notice = fmt.Sprintf("%s left the merge queue: %s.", q.PR, r.Reason)
	}
	return step(q, s)
}
