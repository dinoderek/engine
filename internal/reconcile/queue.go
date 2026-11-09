package reconcile

import (
	"fmt"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hostops"
)

// The merge queue's bounds. Each wait is fixed here, so no request waits
// silently and none holds its repository's queue for longer.
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
	// GhFailures is how many failed merge calls in a row hold the head for
	// the operator.
	GhFailures = 3
	// MaxDemotions in a row drop an entry from the queue.
	MaxDemotions = 3
	// ProbeEvery is how often a waiting entry's PR is read.
	ProbeEvery = 10 * time.Minute
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
	// Held is true while the operator has not acknowledged the notice that
	// held the entry.
	Held bool
	// Host is where the task's latest attempt ran: its PR merges there.
	Host string
}

// eligible reports whether the entry may be the head.
func (q QueueItem) eligible() bool {
	return q.State == "in-review" && q.Question == 0 && !q.Stopped && !q.Held
}

// Probe is what one host call said of an entry's PR.
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
// first; with none, the host calls: the head's merge and the reads of the
// waiting entries that are due one. A head that stops being eligible gives
// up the head and keeps its place.
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
	}
	var acts []Action
	if head >= 0 {
		q := v.Entries[head]
		switch {
		case q.Phase == control.QueueRefresh && v.Now.Sub(q.TurnAt) >= TurnWait:
			return []Action{demote(q, "it was not refreshed within "+TurnWait.String()+" of its turn", true)}
		case q.Phase == control.QueueCheck:
			acts = append(acts, call(q, false))
		}
	}
	for _, q := range v.Entries {
		if q.Phase == control.QueueWaiting && !q.Stopped && !q.Held && v.Now.Sub(q.ProbedAt) >= ProbeEvery {
			acts = append(acts, call(q, true))
		}
	}
	return acts
}

func call(q QueueItem, check bool) Action {
	return Action{Kind: Merge, Item: q.Task, Host: q.Host, Entry: q.QueueEntry, Check: check, Request: q.Request}
}

func step(q QueueItem, s control.QueueStep) Action {
	s.From = q.QueueEntry
	return Action{Kind: QueueStep, Item: q.Task, Step: &s}
}

// unheaded is e back in the queue, no longer the head.
func unheaded(e control.QueueEntry) control.QueueEntry {
	e.Phase = control.QueueWaiting
	e.HeadAt, e.TurnAt, e.WaitSince, e.FailSince, e.Failures = time.Time{}, time.Time{}, time.Time{}, time.Time{}, 0
	return e
}

// demote sends q to the back of the queue, or drops it after MaxDemotions
// in a row. The executor is told, and the operator when operator is set or
// the entry is dropped.
func demote(q QueueItem, why string, operator bool) Action {
	if q.Demotions+1 >= MaxDemotions {
		why = fmt.Sprintf("it was moved to the back %d times in a row; last, %s", MaxDemotions, why)
		return drop(q, "Dropped from the merge queue: "+why+". Request the merge again once that is fixed.", why)
	}
	to := unheaded(q.QueueEntry)
	to.Demotions++
	to.Reason = why
	s := control.QueueStep{To: &to, Kind: control.MergeDemoted, Demote: true,
		Text: "Moved to the back of the merge queue: " + why + ". Your request stays queued; a newer request keeps this place."}
	if operator {
		s.Notice = fmt.Sprintf("%s moved to the back of the merge queue: %s.", q.PR, why)
	}
	return step(q, s)
}

// drop ends q's entry with a merge-refused answer, telling the executor
// text and the operator why.
func drop(q QueueItem, text, why string) Action {
	return step(q, control.QueueStep{Kind: control.MergeRefused, Text: text,
		Notice: fmt.Sprintf("%s left the merge queue: %s.", q.PR, why)})
}

// Answer is the step that what a host call said of q's PR calls for, if any.
func Answer(now time.Time, q QueueItem, p Probe) []Action {
	if q.Phase == control.QueueWaiting {
		return answerProbe(now, q, p)
	}
	to := q.QueueEntry
	to.FailSince, to.Failures = time.Time{}, 0
	switch {
	case p.Missing:
		held := unheaded(q.QueueEntry)
		held.Reason = "held: host " + q.Host + " is not in hosts.toml"
		return []Action{step(q, control.QueueStep{To: &held, Hold: true, Notice: fmt.Sprintf(
			"Cannot merge %s: its task's host %q is not in hosts.toml. Fix the host config, then acknowledge this message to put it back in the merge queue.",
			q.PR, q.Host)})}
	case p.Unreachable:
		to.Failures = q.Failures
		if q.FailSince.IsZero() {
			to.FailSince = now
			return []Action{step(q, control.QueueStep{To: &to})}
		}
		if now.Sub(q.FailSince) >= HostWait {
			return []Action{demote(q, fmt.Sprintf("host %s was unreachable for %s", q.Host, HostWait), true)}
		}
		return nil
	case p.Err != "":
		to.FailSince, to.Failures = q.FailSince, q.Failures+1
		if to.Failures < GhFailures {
			return []Action{step(q, control.QueueStep{To: &to})}
		}
		held := unheaded(q.QueueEntry)
		held.Reason = "held: " + p.Err
		return []Action{step(q, control.QueueStep{To: &held, Hold: true, Notice: fmt.Sprintf(
			"Merging %s at %s failed %d times on host %s: %s. Check gh auth status and engine setup there; acknowledge this message to put it back in the merge queue.",
			q.PR, q.Head, GhFailures, q.Host, p.Err)})}
	}
	return answerHead(now, q, to, p.Reply)
}

// answerHead is the step for what the head's merge call reported. to is the
// head with its failure counts cleared.
func answerHead(now time.Time, q QueueItem, to control.QueueEntry, r hostops.Merged) []Action {
	healthy := func() []Action {
		if to == q.QueueEntry {
			return nil
		}
		return []Action{step(q, control.QueueStep{To: &to})}
	}
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
			return []Action{demote(q, fmt.Sprintf("its checks were pending for %s (%s)", ChecksWait, r.Reason), true)}
		}
		return healthy()
	case hostops.CodePendingMergeable:
		if q.WaitSince.IsZero() {
			to.WaitSince = now
			return []Action{step(q, control.QueueStep{To: &to})}
		}
		if now.Sub(q.WaitSince) >= MergeableWait {
			return []Action{demote(q, "GitHub did not work out whether it is mergeable within "+MergeableWait.String(), true)}
		}
		return healthy()
	case hostops.CodeBehind, hostops.CodeConflict, hostops.CodeHeadMoved:
		to.Phase, to.TurnAt, to.WaitSince, to.Reason = control.QueueRefresh, now, time.Time{}, r.Reason
		return []Action{step(q, control.QueueStep{To: &to, Kind: control.MergeTurn, Text: fmt.Sprintf(
			"Your turn in the merge queue: %s. Merge the default branch in, rerun the gates and the fresh review, push, then run engine task merge-ready for the new head within %s.",
			r.Reason, TurnWait)})}
	case hostops.CodeReady:
		return healthy()
	}
	return []Action{refused(q, r)}
}

// answerProbe is the step for what a waiting entry's read reported: an
// outcome that ends the entry is acted on before its turn, and a moved head
// is told once, so the executor requests again before its turn.
func answerProbe(now time.Time, q QueueItem, p Probe) []Action {
	to := q.QueueEntry
	to.ProbedAt = now
	if p.Unreachable || p.Err != "" || p.Missing {
		return []Action{step(q, control.QueueStep{To: &to})}
	}
	switch p.Reply.Code {
	case hostops.CodeHeadMoved:
		if q.Reason == p.Reply.Reason {
			break
		}
		to.Reason = p.Reply.Reason
		return []Action{step(q, control.QueueStep{To: &to, Kind: "say", Text: "Your queued merge request is stale: " + p.Reply.Reason +
			". Request the merge again for the new head once it is gated and reviewed; the entry keeps its place."})}
	case hostops.CodeMerged, hostops.CodeMergedElsewhere:
		return []Action{merged(q, p.Reply)}
	case hostops.CodeRed, hostops.CodeClosed, hostops.CodeBlocked, hostops.CodeInvalid:
		return []Action{refused(q, p.Reply)}
	}
	return []Action{step(q, control.QueueStep{To: &to})}
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
