// Package controlrun adapts the existing reconcile decisions to server-owned work.
package controlrun

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/remote"
	"github.com/dinoderek/engine/internal/run"
)

// Coordinator has one loop across all requested plans, so dispatches share fleet capacity.
type Coordinator struct {
	Service control.Service
	URL     string
	Home    string
	Client  func(hosts.Host) remote.Client
	Logf    func(string, ...any)
	// Config is injected by tests; production loads hosts.toml on every pass.
	Config *hosts.Config
	Host   hosts.Host
	Resume func(run.Session) error
	Wake   func(run.Session, string) error
	// Observer, when set, keeps each pass's host reads for the session view.
	Observer *Observer
}

// Run reconciles immediately and then periodically until shutdown.
func (c *Coordinator) Run(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if err := c.Pass(ctx); err != nil {
			c.log("coordinator: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (c *Coordinator) log(format string, args ...any) {
	if c.Logf != nil {
		c.Logf(format, args...)
	}
}

// Pass reconciles persisted intent; host outages leave executions reserved.
func (c *Coordinator) Pass(ctx context.Context) error {
	st, err := c.Service.Store.Snapshot()
	if err != nil {
		return err
	}
	var ids []string
	for id, item := range st.Items {
		if item.Coordinating {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	sort.Slice(ids, func(i, j int) bool { return control.Number(ids[i]) < control.Number(ids[j]) })
	cfg, local, err := c.configuration()
	if err != nil {
		return c.notice("", "configuration", err.Error())
	}
	for _, id := range ids {
		if err = c.passItem(ctx, id, cfg, local); err != nil {
			return err
		}
	}
	return nil
}

func (c *Coordinator) passItem(ctx context.Context, id string, cfg *hosts.Config, local hosts.Host) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	// Each plan sees launches from the previous plan.
	fleet := (run.System{Config: cfg, Host: local, Client: c.Client}).Fleet()
	if err := c.reconcileDispatches(id, fleet, cfg); err != nil {
		return err
	}
	if err := c.merges(id, cfg); err != nil {
		return err
	}
	// Recovery may have launched or adopted a session after the first observation.
	fleet = (run.System{Config: cfg, Host: local, Client: c.Client}).Fleet()
	st, err := c.Service.Store.Snapshot()
	if err != nil {
		return err
	}
	c.Observer.Record(st, fleet)
	if !st.Items[id].Coordinating {
		return nil
	}
	v, err := c.view(st, id, fleet)
	if err != nil {
		return err
	}
	if err = c.wakeMessages(st, v); err != nil {
		c.log("wake messages: %v", err)
	}
	if st.Items[id].State == "done" {
		if err = c.cleanup(st, id, v, cfg); err != nil {
			c.log("cleanup: %v", err)
		}
		return nil
	}
	if len(v.LintProblems) > 0 && st.Items[id].State != "approved" {
		if err = c.notice(id, fmt.Sprintf("lint/%d", st.Items[id].Version), "Plan lint failed: "+fmt.Sprint(v.LintProblems)); err != nil {
			c.log("lint notice: %v", err)
		}
		return nil
	}
	for _, act := range run.Decide(v) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err = c.apply(st, id, act, cfg); err != nil {
			c.log("%s %s: %v", act.Kind, id, err)
		}
	}
	return nil
}

func (c *Coordinator) configuration() (*hosts.Config, hosts.Host, error) {
	if c.Config != nil {
		return c.Config, c.Host, nil
	}
	return hosts.Open(hosts.ConfigDir(c.Home))
}

func (c *Coordinator) notice(id, key, body string) error {
	_, err := c.Service.Execute(control.Actor{Name: "coordinator", Role: "coordinator"}, control.Request{
		Op: "say", ID: id, Key: "notice/" + id + "/" + key, Body: body, Recipient: "operator"})
	return err
}

func (c *Coordinator) change(id, state string) error {
	out, err := c.Service.Execute(control.Actor{Name: "coordinator", Role: "coordinator"}, control.Request{Op: "get", ID: id})
	if err != nil {
		return err
	}
	if out.Item.State == state {
		return nil
	}
	_, err = c.Service.Execute(control.Actor{Name: "coordinator", Role: "coordinator"}, control.Request{
		Op: "state", ID: id, State: state, Version: out.Item.Version, Key: fmt.Sprintf("state/%s/%d/%s", id, out.Item.Version, state)})
	return err
}
