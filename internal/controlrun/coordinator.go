// Package controlrun is the server's coordinator: each pass reads server
// records and host observations into a reconcile View, and applies the
// actions reconcile.Decide returns.
package controlrun

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/reconcile"
	"github.com/dinoderek/engine/internal/remote"
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
	Resume func(reconcile.Session) error
	Wake   func(reconcile.Session, string) error
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
	if err := c.reconcileDispatches(id, c.fleet(cfg, local), cfg); err != nil {
		return err
	}
	// Recovery may have launched or adopted a session after the first observation.
	fleet := c.fleet(cfg, local)
	st, v, acts, err := c.decide(id, fleet, cfg)
	if err != nil {
		return err
	}
	c.Observer.Record(st, fleet)
	if !st.Items[id].Coordinating {
		return nil
	}
	if err = c.keepObservations(v); err != nil {
		return err
	}
	if err = c.wakeMessages(st, v); err != nil {
		c.log("wake messages: %v", err)
	}
	released := true
	for _, act := range acts {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Merges ran first; a stop never abandons a window it failed to release.
		if act.Kind == reconcile.Merge || act.Kind == reconcile.Stop && !released {
			continue
		}
		if err = c.apply(st, act, cfg); err != nil {
			c.log("%s %s: %v", act.Kind, act.Item, err)
			released = released && act.Kind != reconcile.Release
		}
	}
	return nil
}

// decide is the pass's decision for root id. Merges are applied first, and
// when there were any the pass is decided again on the records they
// changed: a merged task is done for the rest of the pass.
func (c *Coordinator) decide(id string, fleet reconcile.Fleet, cfg *hosts.Config) (control.State, reconcile.View, []reconcile.Action, error) {
	st, err := c.Service.Store.Snapshot()
	if err != nil || !st.Items[id].Coordinating {
		return st, reconcile.View{}, nil, err
	}
	v := c.view(st, id, fleet)
	acts := reconcile.Decide(v)
	merging := false
	for _, act := range acts {
		if act.Kind != reconcile.Merge {
			continue
		}
		merging = true
		if err = c.merge(act, cfg); err != nil {
			c.log("merge %s: %v", act.Item, err)
		}
	}
	if !merging {
		return st, v, acts, nil
	}
	if st, err = c.Service.Store.Snapshot(); err != nil || !st.Items[id].Coordinating {
		return st, reconcile.View{}, nil, err
	}
	v = c.view(st, id, fleet)
	return st, v, reconcile.Decide(v), nil
}

// fleet reads every host's status, one call per host, all at once.
func (c *Coordinator) fleet(cfg *hosts.Config, local hosts.Host) reconcile.Fleet {
	f := reconcile.Fleet{Local: local.Name, DefaultHarness: cfg.DefaultHarness, HarnessRatio: cfg.HarnessRatio, Caps: map[string]int{}, Heavy: cfg.HeavyCapabilities}
	for name, h := range cfg.Harnesses {
		f.Caps[name] = h.MaxSessions
	}
	names := cfg.HostNames()
	f.Hosts = make([]reconcile.HostLoad, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Go(func() {
			h := cfg.Hosts[name]
			st, err := c.Client(h).Status()
			f.Hosts[i] = reconcile.Load(h.Name, h.Harnesses, h.Capabilities, st, err)
		})
	}
	wg.Wait()
	return f
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
