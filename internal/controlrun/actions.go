package controlrun

import (
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/reconcile"
)

// apply does one action of a pass decided on st. A Merge is applied by
// merge, before the rest of the pass.
func (c *Coordinator) apply(st control.State, a reconcile.Action, cfg *hosts.Config) error {
	switch a.Kind {
	case reconcile.Dispatch:
		return c.dispatch(st, st.Items[a.Item], a.Host, a.Harness, a.Resume, cfg)
	case reconcile.SetState:
		if err := c.change(a.Item, a.State); err != nil || a.Key == "" {
			return err
		}
		return c.notice(a.Item, a.Key, a.Text)
	case reconcile.EndAttempt:
		at, err := c.Service.EndDead(a.Attempt, a.Text)
		if err != nil {
			return err
		}
		if at.Reason == control.EndVanished {
			return c.notice(a.Item, "vanished/"+at.ID, a.Text+"; the task stays resumable and is dispatched again as a resume")
		}
		return c.notice(a.Item, "failed/"+at.ID, a.Text)
	case reconcile.Notice:
		return c.mark(a)
	case reconcile.Resume:
		return c.resume(a)
	case reconcile.Release:
		return c.release(a, cfg)
	case reconcile.Stop:
		return c.stop(a.Item)
	}
	return nil
}

// mark sends a notice, then records on its attempt what it flagged: the
// stall's start or wait, or the limit reset it alerted.
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

// resume types continue once per reset, after rechecking that the attempt
// is current and no hard stop parks it.
func (c *Coordinator) resume(a reconcile.Action) error {
	if c.Resume == nil {
		return nil
	}
	eligible := false
	err := c.Service.Store.Transaction(func(st *control.State) error {
		current := st.Items[a.Item]
		if control.PendingQuestion(st, current.ID) > 0 {
			return nil
		}
		at := st.Attempts[current.Attempt]
		if current.State == "needs-human" || at.ID != a.Attempt || at.State == "ended" || at.ResumedFor == a.ResetsAt {
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
	return c.Resume(a.Session)
}

func (c *Coordinator) now() time.Time {
	if c.Service.Now != nil {
		return c.Service.Now()
	}
	return time.Now().UTC()
}

// release closes a finished session's window and worktree on its host, and
// ends its attempt.
func (c *Coordinator) release(a reconcile.Action, cfg *hosts.Config) error {
	if _, err := c.Client(cfg.Hosts[a.Session.Host]).Release(a.Session.Name, false); err != nil || a.Attempt == "" {
		return err
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
