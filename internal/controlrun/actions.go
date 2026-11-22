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

// apply does one action of a pass decided on st. A Merge is applied by
// merge, before the rest of the pass.
func (c *Coordinator) apply(st control.State, a reconcile.Action, cfg *hosts.Config) error {
	switch a.Kind {
	case reconcile.Dispatch:
		return c.dispatch(st, a, cfg)
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
// control.DeathCap in a row, fails the task. A death in a host-wide loss says
// nothing more: its host's notice did.
func (c *Coordinator) endAttempt(a reconcile.Action) error {
	if a.AtLimit {
		return c.endAtLimit(a)
	}
	at, err := c.Service.EndDead(a.Attempt, a.Text, a.Tail)
	if err != nil || at.HostLost {
		// A host-wide loss told the operator once for the host.
		return err
	}
	st, err := c.Service.Store.Snapshot()
	if err != nil {
		return err
	}
	if st.Items[a.Item].Kind == "plan" {
		return c.plannerEnded(st, a, at)
	}
	switch {
	case at.Reason == control.EndVanished:
		return c.notice(a.Item, "vanished/"+at.ID, a.Text+"; the task stays resumable and is dispatched again as a resume")
	case at.Reason == control.EndEarlyDeath:
		return c.notice(a.Item, "early/"+at.ID, a.Text+" before it claimed the task; the task is dispatched again once"+tails([]control.Attempt{at}))
	case at.Early:
		return c.notice(a.Item, "failed/"+at.ID, a.Text+" before it claimed the task, for the second time; the task failed"+tails(earlyAttempts(st, at)))
	case at.Crashed:
		return c.notice(a.Item, "failed/"+at.ID, fmt.Sprintf("%s after claiming the task, %d such deaths in a row; the task failed%s",
			a.Text, control.DeathCap, tails(lastCrashes(st, at))))
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
	st, err := c.Service.Store.Snapshot()
	if err != nil {
		return err
	}
	if st.Items[a.Item].Kind == "plan" {
		return c.plannerAtLimit(st, a, at)
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

// earlyAttempts are the attempts of at's task that died before claiming it,
// oldest first, at the newest.
func earlyAttempts(st control.State, at control.Attempt) []control.Attempt {
	return attemptsOf(st, at, func(o control.Attempt) bool { return o.Early })
}

// lastCrashes are the last two crashes of at's item, at the newest: the
// deaths the cap counted last.
func lastCrashes(st control.State, at control.Attempt) []control.Attempt {
	out := attemptsOf(st, at, func(o control.Attempt) bool { return o.Crashed })
	return out[max(0, len(out)-2):]
}

// attemptsOf are the attempts of at's item that keep, oldest first.
func attemptsOf(st control.State, at control.Attempt, keep func(control.Attempt) bool) []control.Attempt {
	var out []control.Attempt
	for _, o := range st.Attempts {
		if o.Item == at.Item && keep(o) {
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
	return c.Service.Store.Transaction(func(st *control.State) error {
		at := st.Attempts[a.Attempt]
		switch {
		case !a.Stalled.IsZero():
			at.Notice = a.Stalled
		case a.Far:
			at.FarFor = a.Limit
		default:
			at.StuckFor = a.Limit
		}
		st.Attempts[at.ID] = at
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
	err := c.Service.Store.Transaction(func(st *control.State) error {
		current := st.Items[a.Item]
		if control.OwnQuestion(st, current.ID) > 0 {
			return nil
		}
		at = st.Attempts[current.Attempt]
		if current.State == "needs-human" || at.ID != a.Attempt || at.State == "ended" || resumed(at, a.ResetsAt, c.now()) {
			return nil
		}
		at.ResumedFor = a.ResetsAt
		at.ResumeAt = c.now()
		st.Attempts[at.ID] = at
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
// attempt has ended already. A release with a Key tells the operator, once,
// and so does a done task's release whose host replied with a note, such as
// the repo's own worktree release failing.
func (c *Coordinator) release(a reconcile.Action, cfg *hosts.Config) error {
	outcome := hostops.RunEnded
	switch {
	case a.Done:
		outcome = hostops.RunDone
	case a.Stopped:
		outcome = hostops.RunStopped
	}
	reply, err := c.Client(cfg.Hosts[a.Session.Host]).Release(a.Session.Name, outcome, a.Head)
	if err != nil {
		return err
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

// stop clears root's coordination intent. Completed and failed intent stays
// persisted; it cannot redispatch a task.
func (c *Coordinator) stop(root string) error {
	out, err := c.Service.Execute(control.Actor{Name: "coordinator", Role: "coordinator"}, control.Request{Op: "get", ID: root})
	if err != nil {
		return err
	}
	return c.Service.Store.Transaction(func(s *control.State) error {
		i := s.Items[root]
		if i.Version != out.Item.Version {
			return nil
		}
		i.Coordinating = false
		i.Version++
		s.Items[root] = i
		return nil
	})
}
