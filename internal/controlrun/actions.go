package controlrun

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/reconcile"
)

// apply does one action of a pass decided on tx. A Merge is applied by
// merge, before the rest of the pass.
func (c *Coordinator) apply(tx *control.Tx, a reconcile.Action, cfg *hosts.Config) error {
	switch a.Kind {
	case reconcile.Dispatch:
		return c.dispatch(tx, a, cfg)
	case reconcile.SetState:
		if err := c.change(a.Item, a.State); err != nil || a.Key == "" {
			return err
		}
		return c.notice(a.Item, a.Key, a.Text)
	case reconcile.EndAttempt:
		return c.endAttempt(a)
	case reconcile.Notice:
		return c.mark(a)
	case reconcile.Resume:
		return c.resume(a, cfg)
	case reconcile.Release:
		return c.release(a, cfg)
	case reconcile.Stop:
		return c.stop(a.Item)
	}
	return nil
}

// endAttempt ends a dead session's attempt and tells the operator how: a
// vanished task resumes, a session that died before claiming its task is
// dispatched again once, and a second early death, or the crash that makes
// control.DeathCap in a row, fails the task. A death while an upgrade ran is
// reported as an engine bug, with its tail. Any other death in a host-wide
// loss says nothing more: its host's notice did.
func (c *Coordinator) endAttempt(a reconcile.Action) error {
	if a.AtLimit {
		return c.endAtLimit(a)
	}
	at, err := c.Service.EndDead(a.Attempt, a.Text, a.Tail)
	if err == nil && at.Upgraded {
		return c.notice(a.Item, "upgrade-death/"+at.ID, a.Text+" while engine upgrade ran. No session should die from an upgrade, so this is an engine bug: "+
			"report it, with the tail below. The death counts toward no limit, and the work resumes"+tails([]control.Attempt{at}))
	}
	if err != nil || at.HostLost {
		// A host-wide loss told the operator once for the host.
		return err
	}
	plan, history, err := c.history(a.Item)
	if err != nil {
		return err
	}
	if plan {
		return c.plannerEnded(history, a, at)
	}
	switch {
	case at.Reason == control.EndVanished:
		return c.notice(a.Item, "vanished/"+at.ID, a.Text+"; the task stays resumable and is dispatched again as a resume")
	case at.Reason == control.EndEarlyDeath:
		return c.notice(a.Item, "early/"+at.ID, a.Text+" before it claimed the task; the task is dispatched again once"+tails([]control.Attempt{at}))
	case at.Early:
		return c.notice(a.Item, "failed/"+at.ID, a.Text+" before it claimed the task, for the second time; the task failed"+tails(earlyAttempts(history)))
	case at.Crashed:
		return c.notice(a.Item, "failed/"+at.ID, fmt.Sprintf("%s after claiming the task, %d such deaths in a row; the task failed%s",
			a.Text, control.DeathCap, tails(lastCrashes(history))))
	}
	return c.notice(a.Item, "failed/"+at.ID, a.Text)
}

// endAtLimit ends the attempt of a session that died at a usage limit,
// resumable, and tells the operator when its task is dispatched again: a
// probe's end, which only restarts the wait, tells no one.
func (c *Coordinator) endAtLimit(a reconcile.Action) error {
	at, err := c.Service.EndAtLimit(a.Attempt, a.Text, a.ResetsAt)
	if err != nil || a.Probe {
		return err
	}
	plan, history, err := c.history(a.Item)
	if err != nil {
		return err
	}
	if plan {
		return c.plannerAtLimit(history, a, at)
	}
	return c.notice(a.Item, "limit-death/"+at.ID, fmt.Sprintf("%s; the task stays resumable and is dispatched again %s, from its own branch. "+
		"Until then no task starts on %s on %s; another host or harness with quota may take it.", a.Text, limitWhen(at), at.Harness, at.Host))
}

// limitWhen says when the item of at, ended at a usage limit, is dispatched
// again.
func limitWhen(at control.Attempt) string {
	if reconcile.KnownReset(at.ResetsAt, at.Ended) {
		return "at " + time.Unix(at.ResetsAt, 0).Add(reconcile.ResumeDelay).UTC().Format(time.RFC3339) + ", once the reset has passed"
	}
	return fmt.Sprintf("by a probe every %d minutes, the reset being unknown", int(reconcile.ProbeEvery.Minutes()))
}

// history is whether item is a plan, and all its attempts, as the records
// hold them now.
func (c *Coordinator) history(item string) (plan bool, attempts []control.Attempt, err error) {
	err = read(c.Service.Store, func(tx *control.Tx) error {
		it, _ := tx.Item(item)
		plan, attempts = it.Kind == "plan", tx.AttemptsFor(item)
		return nil
	})
	return plan, attempts, err
}

// earlyAttempts are the attempts of at's task that died before claiming it,
// oldest first, at the newest.
func earlyAttempts(history []control.Attempt) []control.Attempt {
	return attemptsOf(history, func(o control.Attempt) bool { return o.Early })
}

// lastCrashes are the last two crashes of at's item, at the newest: the
// deaths the cap counted last.
func lastCrashes(history []control.Attempt) []control.Attempt {
	out := attemptsOf(history, func(o control.Attempt) bool { return o.Crashed })
	return out[max(0, len(out)-2):]
}

// attemptsOf are the attempts of one item among history that keep, oldest
// first.
func attemptsOf(history []control.Attempt, keep func(control.Attempt) bool) []control.Attempt {
	var out []control.Attempt
	for _, o := range history {
		if keep(o) {
			out = append(out, o)
		}
	}
	slices.SortFunc(out, func(x, y control.Attempt) int { return control.Number(x.ID) - control.Number(y.ID) })
	return out
}

// tails is each attempt's pane log tail, for a notice.
func tails(attempts []control.Attempt) string {
	var b strings.Builder
	for _, at := range attempts {
		fmt.Fprintf(&b, "\n\nPane log tail of %s:\n%s", at.ID, cmp.Or(at.Tail, "(the host showed no pane log)"))
	}
	return b.String()
}

// mark sends a notice, then records on its attempt what it flagged: the
// stall's start or wait, the last activity of a quiet session, or the limit
// reset it alerted.
func (c *Coordinator) mark(a reconcile.Action) error {
	if err := c.notice(a.Item, a.Key, a.Text); err != nil || a.Attempt == "" {
		return err
	}
	if a.Stalled.IsZero() && a.Limit == 0 {
		return nil
	}
	return write(c.Service.Store, func(tx *control.Tx) error {
		at, _ := tx.Attempt(a.Attempt)
		switch {
		case !a.Stalled.IsZero():
			at.Notice = a.Stalled
		case a.Far:
			at.FarFor = a.Limit
		default:
			at.StuckFor = a.Limit
		}
		tx.PutAttempt(at)
		return nil
	})
}

// ResumeText is what a resume types into a session stopped at a usage
// limit.
const ResumeText = "continue"

// resume types continue once per reset, after rechecking that the attempt
// is current and no hard stop parks it.
func (c *Coordinator) resume(a reconcile.Action, cfg *hosts.Config) error {
	if c.Resume == nil {
		return nil
	}
	eligible := false
	var at control.Attempt
	err := write(c.Service.Store, func(tx *control.Tx) error {
		current, _ := tx.Item(a.Item)
		if control.OwnQuestion(tx, current.ID) > 0 {
			return nil
		}
		at, _ = tx.Attempt(current.Attempt)
		if current.State == "needs-human" || at.ID != a.Attempt || at.State == "ended" || resumed(at, a.ResetsAt, c.now()) {
			return nil
		}
		at.ResumedFor = a.ResetsAt
		at.ResumeAt = c.now()
		tx.PutAttempt(at)
		eligible = true
		return nil
	})
	if err != nil || !eligible {
		return err
	}
	return c.Resume(a.Session, func(text string) error { return c.typed(cfg, at, text) })
}

// resumed reports whether at was already told to continue: once per known
// reset, and once per ProbeEvery for an unknown one.
func resumed(at control.Attempt, reset int64, now time.Time) bool {
	if reset > 0 {
		return at.ResumedFor == reset
	}
	return at.ResumedFor == 0 && !at.ResumeAt.IsZero() && now.Sub(at.ResumeAt) < reconcile.ProbeEvery
}

func (c *Coordinator) now() time.Time {
	if c.Service.Now != nil {
		return c.Service.Now()
	}
	return time.Now().UTC()
}

// release closes a finished session's window and worktree on its host, and
// ends its attempt. A stopped task's keeps the worktree and branch, and its
// attempt has ended already; so does a moved one's, after pushing its
// branch. A release with a Key tells the operator, once, and so does a done
// task's release whose host replied with a note, such as the repo's own
// worktree release failing, and a moved one's that left work origin lacks.
func (c *Coordinator) release(a reconcile.Action, cfg *hosts.Config) error {
	outcome := hostops.RunEnded
	switch {
	case a.Done:
		outcome = hostops.RunDone
	case a.Stopped:
		outcome = hostops.RunStopped
	case a.Moved:
		outcome = hostops.RunMoved
	}
	reply, err := c.Client(cfg.Hosts[a.Session.Host]).Release(a.Session.Name, outcome, a.Head)
	if err != nil {
		return err
	}
	if a.Moved {
		if err := c.movedLeft(a, reply); err != nil {
			return err
		}
		return c.Service.MovedReleased(a.Attempt)
	}
	if a.Key != "" {
		if err := c.notice(a.Item, a.Key, a.Text); err != nil {
			return err
		}
	}
	if a.Done && reply.Note != "" && a.Attempt != "" {
		if err := c.notice(a.Item, "release-note/"+a.Attempt, fmt.Sprintf("Releasing %s on host %s left a note: %s", a.Session.Name, a.Session.Host, reply.Note)); err != nil {
			return err
		}
	}
	if a.Attempt == "" {
		return nil
	}
	return c.Service.SetAttempt(a.Attempt, "ended", "")
}

// movedLeft tells the operator, once per moved attempt, what releasing its
// window left that origin lacks. A move back onto the same host is told
// nothing: its resume opens on the kept worktree and branch.
func (c *Coordinator) movedLeft(a reconcile.Action, reply hostops.Released) error {
	if reply.Unpushed == "" {
		return nil
	}
	var moveHost string
	if err := read(c.Service.Store, func(tx *control.Tx) error {
		at, _ := tx.Attempt(a.Attempt)
		moveHost = at.MoveHost
		return nil
	}); err != nil {
		return err
	}
	if moveHost == a.Session.Host {
		return nil
	}
	return c.notice(a.Item, "moved-left/"+a.Attempt, fmt.Sprintf(
		"The moved task's old session %s on host %s was released, keeping its worktree there. What origin lacks: %s. A resume on another host starts without it.",
		a.Session.Name, a.Session.Host, reply.Unpushed))
}

// stop clears root's coordination intent. Completed and failed intent stays
// persisted; it cannot redispatch a task.
func (c *Coordinator) stop(root string) error {
	out, err := c.Service.Execute(control.Actor{Name: "coordinator", Role: "coordinator"}, control.Request{Op: "get", ID: root})
	if err != nil {
		return err
	}
	return write(c.Service.Store, func(tx *control.Tx) error {
		i, _ := tx.Item(root)
		if i.Version != out.Item.Version {
			return nil
		}
		i.Coordinating = false
		i.Version++
		tx.PutItem(i)
		return nil
	})
}
