package control

import (
	"slices"
	"time"
)

// ClaimWait is how long a started session has to claim its task: write to
// the server, or stay live. A window that dies sooner without either ended
// before it did any work.
const ClaimWait = 5 * time.Minute

// The reasons an attempt ends: a confirmed-dead window, or the operator.
const (
	// EndVanished leaves the task resumable: its next dispatch is a resume.
	EndVanished = "vanished"
	// EndEarlyDeath is a first death before the session claimed its task: the
	// task goes back to todo, for the next pass to dispatch a new attempt. A
	// second early death of the task ends failed, with Early set. A planner
	// that wrote no task ends the same way (endPlanner).
	EndEarlyDeath = "early-death"
	// EndFailed marks the task failed; only the operator moves it to todo.
	EndFailed = "failed"
	// EndStopped is the operator's stop: the task is todo and held, and its
	// worktree and branch are kept for the resume.
	EndStopped = "stopped"
	// EndMoved is the operator's move: the task keeps its state and merge
	// queue place, and its next dispatch is a resume where the move names
	// (Attempt.MoveHost, MoveHarness).
	EndMoved = "moved"
)

// DeathCap is how many claimed deaths in a row fail a task, or stop the
// coordination of a plan whose planner wrote tasks.
const DeathCap = 3

// stoppedSince reports whether coordination of item's plan stopped after
// attempt at was last seen live, or after it started when never seen live,
// as when a host reboots while the operator has a plan stopped.
func stoppedSince(tx *Tx, item Item, at Attempt) bool {
	stopped := Root(tx, item).StoppedAt
	last := at.Started
	if at.LiveAt.After(last) {
		last = at.LiveAt
	}
	return !stopped.IsZero() && !stopped.Before(last)
}

// claimedDeaths counts the claimed deaths that ran in a row just before at,
// among the attempts of its item started at or after since. Newest first, an
// uncounted attempt is skipped; any other ending, such as an early death, a
// session's own release after its work was done, or the operator's stop or
// retry, ends the run.
func claimedDeaths(tx *Tx, at Attempt, since time.Time) int {
	var earlier []Attempt
	for _, o := range tx.AttemptsFor(at.Item) {
		if Number(o.ID) < Number(at.ID) && !o.Started.Before(since) {
			earlier = append(earlier, o)
		}
	}
	slices.SortFunc(earlier, func(x, y Attempt) int { return Number(y.ID) - Number(x.ID) })
	n := 0
	for _, o := range earlier {
		switch {
		case o.Crashed && o.Reason == EndVanished:
			n++
		case uncounted(o):
		default:
			return n
		}
	}
	return n
}

// uncounted reports whether ended attempt o neither counts toward DeathCap
// nor resets the count: it never opened a window, it died at a usage limit,
// in a host-wide loss or while an upgrade ran, the operator moved it, or it
// ended vanished for another reason, such as coordination stopped while it
// ran.
func uncounted(o Attempt) bool {
	return o.Window == "" || o.Limited || o.HostLost || o.Upgraded || o.Reason == EndMoved || o.Reason == EndVanished && !o.Crashed
}

// Vanished reports whether item is an unfinished task whose current attempt
// ended vanished, in an early death or by the operator's move, so
// coordination dispatches it again as a resume.
func Vanished(tx *Tx, item Item) bool {
	at, ok := tx.Attempt(item.Attempt)
	return ok && item.Kind == "task" && at.State == "ended" && (at.Reason == EndVanished || at.Reason == EndEarlyDeath || at.Reason == EndMoved) &&
		item.State != "done" && item.State != "failed"
}

// Claimed reports whether at's session claimed its task: it wrote to the
// server, or its window was seen live for ClaimWait.
func Claimed(tx *Tx, at Attempt) bool {
	return claimedBy(tx.Wrote, at)
}

// claimed is Claimed.
func claimed(tx *Tx, at Attempt) bool { return Claimed(tx, at) }

// claimedBy is Claimed, given whether an author wrote an update. The window
// test comes first, so most attempts need no query.
func claimedBy(wrote func(author string) bool, at Attempt) bool {
	return !at.LiveAt.IsZero() && at.LiveAt.Sub(at.Started) >= ClaimWait || wrote(at.ID)
}

// earlyDeaths counts the earlier attempts of at's task that died before
// claiming it.
func earlyDeaths(tx *Tx, at Attempt) int {
	n := 0
	for _, o := range tx.AttemptsFor(at.Item) {
		if o.ID != at.ID && o.Early {
			n++
		}
	}
	return n
}

// endDead ends at, whose window a host read confirmed gone. An unfinished
// task's current attempt ends by endTask, a planner's by endPlanner. Only an
// attempt that died early, died unclaimed in a host-wide loss, died while an
// upgrade ran, or crashed keeps its pane log tail.
func (s Service) endDead(tx *Tx, a Actor, at Attempt, body string) Attempt {
	item := tx.item(at.Item)
	at.State, at.Ended = "ended", s.now()
	if item.Kind == "plan" && item.Attempt == at.ID && item.State == "approved" {
		s.endPlanner(tx, item, &at)
	}
	if item.Kind == "task" && item.Attempt == at.ID && item.State != "done" && item.State != "failed" {
		s.endTask(tx, a, item, &at)
	}
	keepTail := at.Early || at.Crashed || at.Upgraded || at.HostLost && at.Reason == EndEarlyDeath
	if !keepTail {
		at.Tail = ""
	}
	tx.PutAttempt(at)
	if at.Reason != "" {
		body = at.Reason + ": " + body
	}
	tx.AppendUpdate(a, Request{ID: at.Item}, "execution-ended", body, s.now())
	return at
}

// endTask sets how at, the current attempt of unfinished task item, ends,
// by the first rule that applies:
//
//  1. it died at a usage limit (at.Limited), or coordination stopped after
//     it was last seen live: vanished, uncounted;
//  2. it went in a host-wide loss (at.HostLost), or died while an upgrade
//     ran (at.Upgraded, set here): vanished if its session claimed the task,
//     else an early death with Early unset, a running task back in todo;
//     neither counts toward the cap or toward the two strikes;
//  3. its session claimed the task: vanished, a crash, unless the
//     DeathCap-1 counted deaths before it were crashes too; then failed;
//  4. otherwise an early death the first time, with a running task back in
//     todo, and failed the second.
//
// A vanished task keeps its state and its merge queue entry, and is
// dispatched again as a resume; a failed one leaves the queue.
func (s Service) endTask(tx *Tx, a Actor, item Item, at *Attempt) {
	switch {
	case at.Limited || stoppedSince(tx, item, *at):
		at.Reason = EndVanished
	case s.markUpgraded(tx, at) || at.HostLost:
		at.Reason = EndVanished
		if !claimed(tx, *at) {
			at.Reason = EndEarlyDeath
			backToTodo(tx, item)
		}
	case claimed(tx, *at):
		at.Crashed, at.Reason = true, EndVanished
		if claimedDeaths(tx, *at, time.Time{}) >= DeathCap-1 {
			at.Reason = EndFailed
			s.failTask(tx, a, item)
		}
	case earlyDeaths(tx, *at) == 0:
		at.Early, at.Reason = true, EndEarlyDeath
		backToTodo(tx, item)
	default:
		at.Early, at.Reason = true, EndFailed
		s.failTask(tx, a, item)
	}
}

// markUpgraded sets at.Upgraded when at died while an upgrade ran, and
// reports it.
func (s Service) markUpgraded(tx *Tx, at *Attempt) bool {
	at.Upgraded = duringUpgrade(tx, *at, s.now())
	return at.Upgraded
}

// backToTodo returns a running task to todo, for the next pass to dispatch
// it again.
func backToTodo(tx *Tx, item Item) {
	if item.State == "running" {
		item.State = "todo"
		item.Version++
		tx.PutItem(item)
	}
}

// failTask fails item after its session's death, withdrawing it from the
// merge queue.
func (s Service) failTask(tx *Tx, a Actor, item Item) {
	withdraw(tx, a, item, "its session failed", s.now())
	item.State = "failed"
	item.Version++
	tx.PutItem(item)
}

// endUnlaunched ends a dispatching attempt that its host confirmed never
// launched: no window and no run record. A launch that never happened is no
// failure of the task, so the attempt ends vanished and a running task
// returns to todo, for the next pass to dispatch a new attempt.
func (s Service) endUnlaunched(tx *Tx, a Actor, at Attempt, body string) Attempt {
	item := tx.item(at.Item)
	at.State, at.Ended, at.Reason = "ended", s.now(), EndVanished
	tx.PutAttempt(at)
	if item.Kind == "task" && item.Attempt == at.ID && item.State == "running" {
		item.State = "todo"
		item.Version++
		tx.PutItem(item)
	}
	tx.AppendUpdate(a, Request{ID: at.Item}, "execution-ended", at.Reason+": "+body, s.now())
	return at
}

// EndDead ends attempt id after the coordinator saw its window gone on a
// host it read, with the tail of its pane log. The tail is kept only when
// the session died before claiming its task, or crashed after claiming it.
// It is idempotent and returns
// the ended attempt.
func (s Service) EndDead(id, body, tail string) (Attempt, error) {
	return s.end(id, body, func(at *Attempt) { at.Tail = tail })
}

// EndAtLimit is EndDead for a window that died with its run's usage limit
// marker set: the attempt ends vanished, recording the limit and its reset
// (zero when unknown), and its task is dispatched again once the reset
// passes (HarnessLimits).
func (s Service) EndAtLimit(id, body string, resetsAt int64) (Attempt, error) {
	return s.end(id, body, func(at *Attempt) { at.Limited, at.ResetsAt = true, resetsAt })
}

// end ends attempt id, set as dead, unless it has ended already.
func (s Service) end(id, body string, set func(*Attempt)) (Attempt, error) {
	var out Attempt
	err := s.write(func(tx *Tx) error {
		at, ok := tx.Attempt(id)
		if !ok {
			return problem(404, "unknown execution")
		}
		if at.State == "ended" {
			out = at
			return nil
		}
		set(&at)
		out = s.endDead(tx, Actor{Name: "coordinator", Role: "coordinator"}, at, body)
		return nil
	})
	return out, err
}
