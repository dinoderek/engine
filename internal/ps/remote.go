package ps

import (
	"errors"
	"slices"
	"sync"

	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/tracker"
)

// maxParallel bounds the status calls in flight. Each call is one ssh with a
// connect timeout, and hosts.toml lists few hosts, so the bound is a guard
// against a claim naming many hosts, not a tuning knob.
const maxParallel = 8

// view is what one host says about its sessions. A failure is an error, and
// an error is unknown, never dead.
type view interface {
	// window reports whether the host has a live window of this name.
	window(name string) (bool, error)
	// agent finds the live Claude Code session of a claim, nil if none.
	agent(t tracker.ClaimedTask) (*session.Agent, error)
}

// remoteView is another host's answer to one status call.
type remoteView struct {
	p      *probes
	host   string
	status hostops.Status
	err    error // the status call failed: nothing the host would say is known
}

func (v remoteView) window(name string) (bool, error) {
	if v.err != nil {
		return false, v.err
	}
	return slices.ContainsFunc(v.status.Windows, func(w hostops.Window) bool { return w.Name == name && w.Alive }), nil
}

// agent matches a session by name only: the host's git checkouts are not
// part of its status, so a session started by hand stays unknown.
func (v remoteView) agent(t tracker.ClaimedTask) (*session.Agent, error) {
	if v.err != nil {
		return nil, v.err
	}
	if v.status.AgentsError != "" {
		err := errors.New(v.status.AgentsError)
		v.p.warn("claude agents on "+v.host, err)
		return nil, err
	}
	for i := range v.status.Agents {
		if v.status.Agents[i].Name == t.Claim.Name {
			return &v.status.Agents[i], nil
		}
	}
	return nil, nil
}

// remoteHosts is the hosts, other than this one, that hold a claim.
func remoteHosts(tasks []tracker.ClaimedTask, local string) []string {
	var names []string
	for _, t := range tasks {
		if h := t.Claim.Host; h != "" && h != local && !slices.Contains(names, h) {
			names = append(names, h)
		}
	}
	slices.Sort(names)
	return names
}

// statuses calls status on each host, in parallel, at most maxParallel at a
// time, and returns each host's view. A failed call warns once and leaves the
// view in error.
func (p *probes) statuses(names []string) map[string]view {
	views := make([]remoteView, len(names))
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxParallel)
	for i, name := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			views[i].status, views[i].err = p.src.Remote(name)
		}()
	}
	wg.Wait()
	out := make(map[string]view, len(names))
	for i, name := range names {
		views[i].p, views[i].host = p, name
		if views[i].err != nil {
			p.warn("status of "+name, views[i].err)
		}
		out[name] = views[i]
	}
	return out
}
