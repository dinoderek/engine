package controlrun

import (
	"errors"
	"fmt"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/reconcile"
)

// OutageGap is how long a pass may start after the one before it without
// the server counting as unavailable in between.
const OutageGap = 5 * time.Minute

// outage is a gap between two pass starts longer than OutageGap.
type outage struct{ from, to time.Time }

// startPass saves this pass's start before any host is read. A start more
// than OutageGap after the coordinator was last known to run marks an outage, whose pass restarts
// every stall timer; the first pass ever, with nothing saved, marks none.
func (c *Coordinator) startPass() error {
	now := c.now()
	prev, gap, err := c.Service.Store.StartPass(now, OutageGap)
	if err != nil {
		return err
	}
	c.keepOutage = false
	if gap {
		c.outage = &outage{from: prev, to: now}
	}
	return nil
}

// endPass saves the end of the pass, so a pass that ran longer than
// OutageGap is not taken for an outage by the next, and drops the outage once
// every session was offered its wake.
func (c *Coordinator) endPass(err error) {
	if saveErr := c.Service.Store.EndPass(c.now()); saveErr != nil {
		c.log("save the end of the pass: %v", saveErr)
	}
	if err == nil && !c.keepOutage {
		c.outage = nil
	}
}

// wakeOutage tells each idle session of a running task, once for this
// outage, to retry what failed during it. A session waiting on the merge
// queue, at a hard stop or at a dialog is not woken. A session that cannot be
// woken is tried again on the next pass; one already woken for this outage is
// never woken twice, which the attempt records once the wake is sent.
func (c *Coordinator) wakeOutage(v reconcile.View, cfg *hosts.Config) error {
	if c.outage == nil || c.Wake == nil {
		return nil
	}
	key := c.outage.from.UnixNano()
	text := fmt.Sprintf("The engine server was unavailable from %s to %s. Retry any engine command that failed in that time, with its request key.",
		c.outage.from.UTC().Format(time.RFC3339), c.outage.to.UTC().Format(time.RFC3339))
	var errs []error
	for _, t := range v.Sessions() {
		at := t.Current
		if t.Kind != "task" || t.State != "running" || !t.Wakeable() || at.WokenFor == key {
			continue
		}
		if err := c.sendOutage(cfg, t, text, key); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// sendOutage logs and types text into t's session, then records the outage
// it was for on the attempt.
func (c *Coordinator) sendOutage(cfg *hosts.Config, t reconcile.Item, text string, key int64) error {
	if err := c.typed(cfg, t.Current, text); err != nil {
		return err
	}
	if err := c.Wake(*t.Session, text); err != nil {
		return err
	}
	return c.Service.Store.Transaction(func(s *control.State) error {
		current := s.Attempts[t.Current.ID]
		current.WokenFor = key
		s.Attempts[t.Current.ID] = current
		return nil
	})
}
