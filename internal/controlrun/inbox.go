package controlrun

import (
	"fmt"
	"slices"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/reconcile"
)

// wakeMessages points each idle session at the updates it has not been
// woken for: one waiting for input, or one only a background job keeps
// busy. A session busy in a turn, limited, or at a hard stop is left alone.
func (c *Coordinator) wakeMessages(st control.State, v reconcile.View) error {
	if c.Wake == nil {
		return nil
	}
	for _, t := range v.Sessions() {
		sess := *t.Session
		if !t.Wakeable() {
			continue
		}
		at := t.Current
		last := pendingMessage(st, t.Item, at)
		if last == 0 {
			continue
		}
		text := fmt.Sprintf("Read the new task updates: engine updates %s --after %d", t.ID, at.Seen)
		if t.Kind == "task" {
			text = fmt.Sprintf("Read the new task and plan updates: engine updates %s --after %d; engine updates %s --after %d",
				t.ID, at.Seen, t.Plan, at.Seen)
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
// message on its own item, or a message or resolution on its task's plan
// that is addressed to the plan's sessions rather than the operator. A session's
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

var itemWakeKinds = []string{"say", "reply", "resolved", control.MergeMerged, control.MergeRefused,
	control.MergeTurn, control.MergeWithdrawn, control.LockRetired}
var planWakeKinds = []string{"say", "resolved"}

func planWake(item control.Item, u control.Update) bool {
	return item.Kind == "task" && u.Item == item.Plan &&
		u.Author != "coordinator" && u.Recipient != "operator" && slices.Contains(planWakeKinds, u.Kind)
}
