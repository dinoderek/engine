package controlrun

import (
	"fmt"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/run"
)

func (c *Coordinator) wakeMessages(st control.State, v run.View) error {
	if c.Wake == nil {
		return nil
	}
	for ref, sess := range v.Sessions {
		item, ok := find(st, ref)
		if !ok || !sess.Live || !sess.Waiting || sess.Limited || sess.Unknown || item.State == "needs-human" {
			continue
		}
		at := st.Attempts[item.Attempt]
		last := pendingMessage(st, item.ID, at)
		if last == 0 {
			continue
		}
		text := fmt.Sprintf("Read the new task updates: engine updates %s --after %d", item.ID, at.Seen)
		if err := c.Wake(sess, text); err != nil {
			return err
		}
		if err := c.Service.Store.Transaction(func(s *control.State) error {
			current := s.Attempts[at.ID]
			if current.Seen < last {
				current.Seen = last
				s.Attempts[at.ID] = current
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

func pendingMessage(st control.State, id string, at control.Attempt) int {
	last := 0
	for _, u := range st.Updates {
		if u.Item != id || u.ID <= at.Seen || u.Author == at.ID {
			continue
		}
		if u.Kind == "say" || u.Kind == "reply" || u.Kind == "resolved" {
			last = u.ID
		}
	}
	return last
}

func (c *Coordinator) cleanup(st control.State, root string, v run.View, cfg *hosts.Config) error {
	pending := false
	for ref, sess := range v.Sessions {
		item, ok := find(st, ref)
		if !ok {
			continue
		}
		at := st.Attempts[item.Attempt]
		if at.State == "ended" {
			continue
		}
		if sess.Unknown || sess.Live && !sess.Waiting {
			pending = true
			continue
		}
		if _, err := c.Client(cfg.Hosts[at.Host]).Release(sess.Name, false); err != nil {
			return err
		}
		if err := c.Service.SetAttempt(at.ID, "ended", ""); err != nil {
			return err
		}
	}
	if pending {
		return nil
	}
	return c.Service.Store.Transaction(func(s *control.State) error {
		item := s.Items[root]
		item.Coordinating = false
		item.Version++
		s.Items[root] = item
		return nil
	})
}
