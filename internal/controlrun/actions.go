package controlrun

import (
	"fmt"
	"strings"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/run"
)

func (c *Coordinator) apply(st control.State, root string, a run.Action, cfg *hosts.Config) error {
	item, ok := find(st, a.Ref)
	if !ok {
		return fmt.Errorf("unknown coordinator reference")
	}
	switch a.Kind {
	case run.StartTask, run.StartPlanner:
		host, harness := a.Host, a.Harness
		if a.Kind == run.StartPlanner {
			host = c.Host.Name
			if host == "" {
				_, h, _ := c.configuration()
				host = h.Name
			}
			harness = cfg.DefaultHarness
		}
		return c.dispatch(st, item, host, harness, cfg)
	case run.SetLabel:
		return c.change(item.ID, strings.TrimPrefix(a.Label, "engine:"))
	case run.Fail:
		if err := c.Service.SetAttempt(item.Attempt, "ended", ""); err != nil {
			return err
		}
		if err := c.change(item.ID, "failed"); err != nil {
			return err
		}
		return c.notice(item.ID, "failed/"+item.Attempt, a.Text)
	case run.ReportLint:
		return c.notice(item.ID, "lint/"+fmt.Sprint(item.Version), "Plan lint failed; no tasks were dispatched. Fix the cards and restart coordination.")
	case run.LimitAlert:
		return c.limitAlert(item, a)
	case run.Stall:
		if err := c.notice(item.ID, fmt.Sprintf("%s/%s/%d", a.Kind, item.Attempt, a.At.Unix()), a.Text); err != nil {
			return err
		}
		return c.Service.Store.Transaction(func(s *control.State) error {
			at := s.Attempts[item.Attempt]
			at.Notice = a.At
			s.Attempts[at.ID] = at
			return nil
		})
	case run.Resume:
		return c.resume(item, a)
	case run.Reap:
		_, err := c.Client(cfg.Hosts[a.Session.Host]).Release(a.Session.Name, false)
		if err == nil && item.Attempt != "" {
			err = c.Service.SetAttempt(item.Attempt, "ended", "")
		}
		return err
	case run.Finish:
		if err := c.change(item.ID, "done"); err != nil {
			return err
		}
		return c.notice(item.ID, "finished", c.summary(st, item.ID))
	case run.Exit:
		out, err := c.Service.Execute(control.Actor{Name: "coordinator", Role: "coordinator"}, control.Request{Op: "get", ID: root})
		if err != nil {
			return err
		}
		// Completed and failed intent stays persisted; it cannot redispatch a task.
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
	case run.Wait:
		if a.Notify {
			return c.notice(item.ID, "placement/"+fmt.Sprint(item.Version), a.Text)
		}
	}
	return nil
}

func (c *Coordinator) resume(item control.Item, a run.Action) error {
	if c.Resume == nil {
		return nil
	}
	eligible := false
	err := c.Service.Store.Transaction(func(st *control.State) error {
		current := st.Items[item.ID]
		if control.PendingQuestion(st, current.ID) > 0 {
			return nil
		}
		at := st.Attempts[current.Attempt]
		if current.State == "needs-human" || at.ID != item.Attempt || at.State == "ended" || at.ResumedFor == a.Session.ResetsAt {
			return nil
		}
		at.ResumedFor = a.Session.ResetsAt
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

func (c *Coordinator) limitAlert(item control.Item, a run.Action) error {
	far := time.Unix(a.Session.ResetsAt, 0).Sub(c.now()) > 24*time.Hour
	kind := "stuck"
	if far {
		kind = "far"
	}
	if err := c.notice(item.ID, fmt.Sprintf("limit/%s/%s/%d", kind, item.Attempt, a.Session.ResetsAt), a.Text); err != nil {
		return err
	}
	return c.Service.Store.Transaction(func(st *control.State) error {
		at := st.Attempts[item.Attempt]
		if far {
			at.FarFor = a.Session.ResetsAt
		} else {
			at.StuckFor = a.Session.ResetsAt
		}
		st.Attempts[at.ID] = at
		return nil
	})
}
