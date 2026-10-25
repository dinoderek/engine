package ps

import (
	"slices"
	"sync"

	"github.com/dinoderek/engine/internal/tracker"
)

// maxParallel bounds the status calls in flight. Each call to another host
// is one ssh with a connect timeout, and hosts.toml lists few hosts, so the
// bound is a guard against claims naming many hosts, not a tuning knob.
const maxParallel = 8

// claimHosts is the hosts that hold a claim, this one included.
func claimHosts(tasks []tracker.ClaimedTask) []string {
	var names []string
	for _, t := range tasks {
		if h := t.Claim.Host; h != "" && !slices.Contains(names, h) {
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
	views := make([]view, len(names))
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxParallel)
	for i, name := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			views[i].status, views[i].err = p.src.Status(name)
		}()
	}
	wg.Wait()
	out := make(map[string]view, len(names))
	for i, name := range names {
		views[i].p, views[i].host, views[i].local = p, name, name == p.src.Local
		if views[i].err != nil {
			p.warn("status of "+name, views[i].err)
		}
		out[name] = views[i]
	}
	return out
}
