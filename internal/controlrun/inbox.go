package controlrun

import (
	"fmt"
	"slices"

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
		if control.PendingQuestion(&st, item.ID) > 0 {
			continue
		}
		at := st.Attempts[item.Attempt]
		last := pendingMessage(st, item, at)
		if last == 0 {
			continue
		}
		text := fmt.Sprintf("Read the new task updates: engine updates %s --after %d", item.ID, at.Seen)
		if item.Kind == "task" && item.Plan != "" {
			text = fmt.Sprintf("Read the new task and plan updates: engine updates %s --after %d; engine updates %s --after %d",
				item.ID, at.Seen, item.Plan, at.Seen)
		}
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

// pendingMessage is the newest update a session has not been woken for: a
// message on its own item, or a message or ruling on its task's plan that
// is addressed to the plan's sessions rather than the operator. A session's
// own updates and the coordinator's notices never wake it.
func pendingMessage(st control.State, item control.Item, at control.Attempt) int {
	last := 0
	for _, u := range st.Updates {
		if u.ID <= at.Seen || u.Author == at.ID {
			continue
		}
		if u.Item == item.ID && slices.Contains(itemWakeKinds, u.Kind) || planWake(item, u) {
			last = u.ID
		}
	}
	return last
}

var itemWakeKinds = []string{"say", "reply", "resolved", control.MergeMerged, control.MergeRefused}
var planWakeKinds = []string{"say", "resolved", "ruling"}

func planWake(item control.Item, u control.Update) bool {
	return item.Kind == "task" && item.Plan != "" && u.Item == item.Plan &&
		u.Author != "coordinator" && u.Recipient != "operator" && slices.Contains(planWakeKinds, u.Kind)
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
		if sess.Unknown || sess.Live && !sess.Waiting || c.justMerged(st, item.ID) {
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
