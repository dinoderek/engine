package controlrun

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/remote"
	"github.com/dinoderek/engine/internal/run"
	"github.com/dinoderek/engine/internal/session"
)

// The session view's observation timing.
const (
	// ObserveEvery is how often the observer looks for stale observations.
	ObserveEvery = 15 * time.Second
	// ObservationFresh is how long an observation, the coordinator's included,
	// is reused before its host is read again.
	ObservationFresh = 45 * time.Second
	// ObservationStale is the age at which an observation reads as unknown.
	ObservationStale = 2 * time.Minute
)

// Observer caches host observations of active attempts, keyed by attempt.
// Requests read only the cache. Refresh reads each host with a stale or
// missing observation in the background, one read per host at a time.
type Observer struct {
	Store  *control.Store
	Config func() (*hosts.Config, error)
	Client func(hosts.Host) remote.Client
	Now    func() time.Time
	Logf   func(string, ...any)

	mu       sync.Mutex
	entries  map[string]observed
	inflight map[string]bool
	reads    sync.WaitGroup
}

type observed struct {
	state    string
	seen, at time.Time
}

func (o *Observer) now() time.Time {
	if o.Now != nil {
		return o.Now().UTC()
	}
	return time.Now().UTC()
}

// Observation is the cached view of an attempt; a stale or missing one is unknown.
func (o *Observer) Observation(id string) control.Observation {
	o.mu.Lock()
	defer o.mu.Unlock()
	e, ok := o.entries[id]
	if !ok {
		return control.Observation{State: "unknown"}
	}
	if o.now().Sub(e.at) >= ObservationStale {
		return control.Observation{State: "unknown", Seen: e.seen}
	}
	return control.Observation{State: e.state, Seen: e.seen}
}

// Record keeps a coordinator pass's fleet reads, so the observer reuses them.
func (o *Observer) Record(st control.State, fleet run.Fleet) {
	if o == nil {
		return
	}
	loads := map[string]run.HostLoad{}
	for _, h := range fleet.Hosts {
		loads[h.Name] = h
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.prune(st)
	for _, at := range activeAttempts(st) {
		h, ok := loads[at.Host]
		o.put(st, at, h, ok)
	}
}

// Refresh starts a read of every host whose active attempts lack a fresh
// observation and that has no read in flight. It never waits for a host.
func (o *Observer) Refresh() error {
	st, err := o.Store.Snapshot()
	if err != nil {
		return err
	}
	o.mu.Lock()
	o.prune(st)
	stale := map[string]bool{}
	for _, at := range activeAttempts(st) {
		if o.needsRead(st, at.Host) {
			stale[at.Host] = true
		}
	}
	o.mu.Unlock()
	if len(stale) == 0 {
		return nil
	}
	cfg, err := o.Config()
	if err != nil {
		return err
	}
	for name := range stale {
		o.start(st, cfg, name)
	}
	return nil
}

func (o *Observer) start(st control.State, cfg *hosts.Config, name string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	// Another refresh may have read the host since the staleness check.
	if !o.needsRead(st, name) {
		return
	}
	h, ok := cfg.Hosts[name]
	if !ok {
		o.putHost(st, run.HostLoad{Name: name}, false)
		return
	}
	o.inflight[name] = true
	o.reads.Add(1)
	go o.read(st, h)
}

// needsRead is true when the host has no read in flight and an active
// attempt there lacks a fresh observation. Callers hold mu.
func (o *Observer) needsRead(st control.State, host string) bool {
	if o.inflight[host] {
		return false
	}
	for _, at := range activeAttempts(st) {
		e, ok := o.entries[at.ID]
		if at.Host == host && (!ok || o.now().Sub(e.at) >= ObservationFresh) {
			return true
		}
	}
	return false
}

func (o *Observer) read(st control.State, h hosts.Host) {
	defer o.reads.Done()
	status, err := o.Client(h).Status()
	l := run.HostLoad{Name: h.Name, Status: status}
	if err != nil {
		l.Unreachable = err.Error()
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.inflight, h.Name)
	o.putHost(st, l, true)
}

// Settle waits for the host reads in flight.
func (o *Observer) Settle() { o.reads.Wait() }

// Run refreshes immediately and then periodically until shutdown.
func (o *Observer) Run(ctx context.Context) {
	ticker := time.NewTicker(ObserveEvery)
	defer ticker.Stop()
	for {
		if err := o.Refresh(); err != nil && o.Logf != nil {
			o.Logf("observer: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// prune drops attempts that ended or were replaced, which bounds the cache. Callers hold mu.
func (o *Observer) prune(st control.State) {
	if o.entries == nil {
		o.entries, o.inflight = map[string]observed{}, map[string]bool{}
	}
	keep := map[string]bool{}
	for _, at := range activeAttempts(st) {
		keep[at.ID] = true
	}
	for id := range o.entries {
		if !keep[id] {
			delete(o.entries, id)
		}
	}
}

func (o *Observer) putHost(st control.State, h run.HostLoad, configured bool) {
	o.prune(st)
	for _, at := range activeAttempts(st) {
		if at.Host == h.Name {
			o.put(st, at, h, configured)
		}
	}
}

// put records one attempt's observation, keeping the last live time. Callers hold mu.
func (o *Observer) put(st control.State, at control.Attempt, h run.HostLoad, configured bool) {
	e := o.entries[at.ID]
	e.at, e.state = o.now(), "unknown"
	if configured {
		item := st.Items[at.Item]
		e.state = hostState(at, h, sessionName(st.Items[item.Repo].Slug, control.Number(item.ID)))
	}
	if slices.Contains([]string{session.StateWorking, session.StateWaiting, session.StateLimited, "live"}, e.state) {
		e.seen = e.at
	}
	o.entries[at.ID] = e
}

func activeAttempts(st control.State) []control.Attempt {
	var out []control.Attempt
	for _, at := range st.Attempts {
		if at.State != "ended" && st.Items[at.Item].Attempt == at.ID {
			out = append(out, at)
		}
	}
	return out
}

// hostState is what a host read says of an attempt. A reachable host without
// the attempt's window confirms its exit, unless the dispatch is unfinished.
func hostState(at control.Attempt, h run.HostLoad, name string) string {
	if h.Unreachable != "" || h.Status.RunsError != "" {
		return "unknown"
	}
	w, ok := matchWindow(at, h.Status, name)
	switch {
	case !ok && at.State == "dispatching":
		return "unknown"
	case !ok || !w.Alive:
		return "exited"
	case slices.Contains([]string{session.StateWorking, session.StateWaiting, session.StateLimited}, w.State):
		return w.State
	}
	return "live"
}

// matchWindow finds an attempt's window by both its id and its session name:
// tmux reuses window ids after a restart.
func matchWindow(at control.Attempt, status hostops.Status, name string) (hostops.Window, bool) {
	for _, w := range status.Windows {
		if w.ID == at.Window && w.Name == name {
			return w, true
		}
	}
	return hostops.Window{}, false
}

func sessionName(slug string, number int) string {
	repo, _ := session.ParseRepo(slug)
	return session.TaskName(repo, number)
}
