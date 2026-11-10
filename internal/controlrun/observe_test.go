package controlrun

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/reconcile"
	"github.com/dinoderek/engine/internal/remote"
	"github.com/dinoderek/engine/internal/session"
)

// probeHost is a concurrency-safe host whose status reads can be held open.
type probeHost struct {
	mu     sync.Mutex
	status hostops.Status
	err    error
	calls  int
	gate   chan struct{}
}

func (h *probeHost) SyncRepo(string) (hostops.Synced, error)             { panic("unused") }
func (h *probeHost) Spawn(hostops.SpawnRequest) (hostops.Spawned, error) { panic("unused") }
func (h *probeHost) Collect(string) (hostops.Collected, error)           { panic("unused") }
func (h *probeHost) Release(string, hostops.Outcome) (hostops.Released, error)      { panic("unused") }
func (h *probeHost) Check(string, io.Writer) (bool, error)               { panic("unused") }
func (h *probeHost) Status() (hostops.Status, error) {
	h.mu.Lock()
	h.calls++
	gate := h.gate
	h.mu.Unlock()
	if gate != nil {
		<-gate
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.status, h.err
}

func (h *probeHost) set(status hostops.Status, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.status, h.err = status, err
}

func (h *probeHost) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls
}

type clock struct {
	mu sync.Mutex
	at time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.at }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

type observerFixture struct {
	svc   control.Service
	obs   *Observer
	clock *clock
	hosts map[string]*probeHost
	repo  control.Item
	plan  control.Item
}

func newObserverFixture(t *testing.T, names ...string) *observerFixture {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 absent")
	}
	store, err := control.Open(filepath.Join(t.TempDir(), "db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	f := &observerFixture{clock: &clock{at: time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)}, hosts: map[string]*probeHost{}}
	f.svc = control.Service{Store: store, Now: f.clock.now}
	cfg := &hosts.Config{Hosts: map[string]hosts.Host{}}
	for _, name := range names {
		cfg.Hosts[name] = hosts.Host{Name: name}
		f.hosts[name] = &probeHost{}
	}
	f.obs = &Observer{Store: store, Now: f.clock.now, Logf: t.Logf,
		Config: func() (*hosts.Config, error) { return cfg, nil },
		Client: func(h hosts.Host) remote.Client { return f.hosts[h.Name] }}
	f.svc.Observe = f.obs.Observation
	f.repo = execute(t, f.svc, control.Request{Op: "create", Item: control.Item{Kind: "repo", Title: "Demo", Slug: "owner/demo", Remote: "ssh://example.test/demo"}})
	f.plan = execute(t, f.svc, control.Request{Op: "create", Item: control.Item{Kind: "plan", Title: "Feature", Repo: f.repo.ID, Body: "Requirements"}})
	f.plan = execute(t, f.svc, control.Request{Op: "approve", ID: f.plan.ID, Version: f.plan.Version})
	return f
}

// running dispatches a new task to host and records its window, as a launch does.
func (f *observerFixture) running(t *testing.T, host, window string) (control.Item, control.Attempt) {
	t.Helper()
	task := execute(t, f.svc, control.Request{Op: "create", Item: control.Item{Kind: "task", Title: "Task", Repo: f.repo.ID, Plan: f.plan.ID, Body: "## Outcome\nWorks"}})
	return task, f.redispatch(t, task, host, window)
}

func (f *observerFixture) redispatch(t *testing.T, task control.Item, host, window string) control.Attempt {
	t.Helper()
	out, err := f.svc.Execute(control.Actor{Name: "operator", Role: "operator"}, control.Request{Op: "claim", Key: control.Secret(), ID: task.ID, Body: host, State: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.svc.SetAttempt(out.Attempt.ID, "running", window); err != nil {
		t.Fatal(err)
	}
	return *out.Attempt
}

func (f *observerFixture) refresh(t *testing.T) {
	t.Helper()
	if err := f.obs.Refresh(); err != nil {
		t.Fatal(err)
	}
	f.obs.Settle()
}

func window(task control.Item, id string, alive bool, state string) hostops.Window {
	return hostops.Window{Name: fmt.Sprintf("demo-%d", control.Number(task.ID)), ID: id, Alive: alive, State: state, Screen: "secret screen"}
}

func expectObservation(t *testing.T, o *Observer, at control.Attempt, state string, seen time.Time) {
	t.Helper()
	if got := o.Observation(at.ID); got.State != state || !got.Seen.Equal(seen) {
		t.Fatalf("%s: got %+v, want %s seen %v", at.ID, got, state, seen)
	}
}

func TestObserverSamplesStoppedCoordinationAndKeepsLastSeen(t *testing.T) {
	f := newObserverFixture(t, "devbox")
	task, at := f.running(t, "devbox", "@1")
	execute(t, f.svc, control.Request{Op: "run", ID: f.plan.ID})
	execute(t, f.svc, control.Request{Op: "stop", ID: f.plan.ID})
	h := f.hosts["devbox"]
	t0 := f.clock.now()
	expectObservation(t, f.obs, at, "unknown", time.Time{})
	h.set(hostops.Status{Windows: []hostops.Window{window(task, "@1", true, session.StateWorking)}}, nil)
	f.refresh(t)
	expectObservation(t, f.obs, at, session.StateWorking, t0)
	f.refresh(t)
	if h.count() != 1 {
		t.Fatalf("a fresh observation was read again: %d reads", h.count())
	}
	steps := []struct {
		status hostops.Status
		err    error
		want   string
	}{
		{err: errors.New("ssh: unreachable"), want: "unknown"},
		{status: hostops.Status{RunsError: "run.json unreadable"}, want: "unknown"},
		{status: hostops.Status{Windows: []hostops.Window{window(task, "@1", false, "")}}, want: "exited"},
		{status: hostops.Status{Windows: []hostops.Window{window(task, "@9", true, session.StateWorking)}}, want: "exited"},
	}
	for _, step := range steps {
		f.clock.add(ObservationFresh)
		h.set(step.status, step.err)
		f.refresh(t)
		expectObservation(t, f.obs, at, step.want, t0)
	}
	f.clock.add(ObservationFresh)
	h.set(hostops.Status{Windows: []hostops.Window{window(task, "@1", true, "")}}, nil)
	f.refresh(t)
	t1 := f.clock.now()
	expectObservation(t, f.obs, at, "live", t1)
	f.clock.add(ObservationStale)
	expectObservation(t, f.obs, at, "unknown", t1)
}

func TestObserverCoalescesHostReadsAndNeverWaitsForAHost(t *testing.T) {
	f := newObserverFixture(t, "devbox", "mac")
	first, a1 := f.running(t, "devbox", "@1")
	second, a2 := f.running(t, "devbox", "@2")
	third, a3 := f.running(t, "mac", "@1")
	_, a4 := f.running(t, "retired", "@1")
	devbox, mac := f.hosts["devbox"], f.hosts["mac"]
	devbox.set(hostops.Status{Windows: []hostops.Window{window(first, "@1", true, session.StateWaiting), window(second, "@2", true, session.StateLimited)}}, nil)
	mac.set(hostops.Status{Windows: []hostops.Window{window(third, "@1", true, session.StateWorking)}}, nil)
	gate := make(chan struct{})
	devbox.gate = gate
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			if err := f.obs.Refresh(); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	// Every refresh returned while the devbox read is still held open.
	expectObservation(t, f.obs, a1, "unknown", time.Time{})
	rows := map[string]control.SessionRow{}
	for _, row := range viewRows(t, f.svc) {
		rows[row.Attempt] = row
	}
	if rows[a1.ID].Observed != "unknown" || len(rows) != 4 {
		t.Fatalf("session view during a slow host read: %+v", rows)
	}
	close(gate)
	f.obs.Settle()
	if devbox.count() != 1 || mac.count() != 1 {
		t.Fatalf("reads per host: devbox %d, mac %d", devbox.count(), mac.count())
	}
	now := f.clock.now()
	expectObservation(t, f.obs, a1, session.StateWaiting, now)
	expectObservation(t, f.obs, a2, session.StateLimited, now)
	expectObservation(t, f.obs, a3, session.StateWorking, now)
	expectObservation(t, f.obs, a4, "unknown", time.Time{})
}

func viewRows(t *testing.T, s control.Service) []control.SessionRow {
	t.Helper()
	out, err := s.Execute(control.Actor{Name: "operator", Role: "operator"}, control.Request{Op: "session-view"})
	if err != nil {
		t.Fatal(err)
	}
	return out.Sessions
}

func TestSessionViewReadsOnlyTheCache(t *testing.T) {
	f := newObserverFixture(t, "devbox")
	task, at := f.running(t, "devbox", "@1")
	h := f.hosts["devbox"]
	h.set(hostops.Status{Windows: []hostops.Window{window(task, "@1", true, session.StateWorking)}}, nil)
	if rows := viewRows(t, f.svc); len(rows) != 1 || rows[0].Observed != "unknown" || h.count() != 0 {
		t.Fatalf("request probed a host: %+v, %d reads", rows, h.count())
	}
	f.refresh(t)
	rows := viewRows(t, f.svc)
	if rows[0].Observed != session.StateWorking || !rows[0].LastSeen.Equal(f.clock.now()) || h.count() != 1 {
		t.Fatalf("cached row: %+v, %d reads", rows, h.count())
	}
	if at.ID != rows[0].Attempt {
		t.Fatalf("row attempt %s, want %s", rows[0].Attempt, at.ID)
	}
}

func TestObserverForgetsEndedAndReplacedAttempts(t *testing.T) {
	f := newObserverFixture(t, "devbox")
	task, old := f.running(t, "devbox", "@1")
	h := f.hosts["devbox"]
	h.set(hostops.Status{Windows: []hostops.Window{window(task, "@1", true, session.StateWorking)}}, nil)
	f.refresh(t)
	if err := f.svc.SetAttempt(old.ID, "ended", ""); err != nil {
		t.Fatal(err)
	}
	current := f.redispatch(t, task, "devbox", "@2")
	h.set(hostops.Status{}, errors.New("host unavailable"))
	f.refresh(t)
	expectObservation(t, f.obs, old, "unknown", time.Time{})
	expectObservation(t, f.obs, current, "unknown", time.Time{})
	if rows := viewRows(t, f.svc); len(rows) != 1 || rows[0].Attempt != current.ID || !rows[0].LastSeen.IsZero() {
		t.Fatalf("replaced attempt row: %+v", rows)
	}
}

func TestObserverReusesCoordinatorReads(t *testing.T) {
	c, coordinatorHost, _, _, task := setup(t)
	probe := &probeHost{}
	c.Observer = &Observer{Store: c.Service.Store, Now: c.Service.Now, Client: func(hosts.Host) remote.Client { return probe },
		Config: func() (*hosts.Config, error) { return c.Config, nil }}
	// The second pass dispatches; the third reads the new window.
	for range 3 {
		pass(t, c)
	}
	st, _ := c.Service.Store.Snapshot()
	at := st.Attempts[st.Items[task.ID].Attempt]
	expectObservation(t, c.Observer, at, session.StateWorking, c.Service.Now())
	if err := c.Observer.Refresh(); err != nil {
		t.Fatal(err)
	}
	c.Observer.Settle()
	if probe.count() != 0 || len(coordinatorHost.launches) != 1 {
		t.Fatalf("observer reread a host the coordinator had just read: %d reads", probe.count())
	}
}

func TestObserverRunRefreshesUntilShutdown(t *testing.T) {
	f := newObserverFixture(t, "devbox")
	task, at := f.running(t, "devbox", "@1")
	f.hosts["devbox"].set(hostops.Status{Windows: []hostops.Window{window(task, "@1", true, session.StateWaiting)}}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f.obs.Run(ctx)
	f.obs.Settle()
	expectObservation(t, f.obs, at, session.StateWaiting, f.clock.now())
	broken := &Observer{Store: f.obs.Store, Logf: t.Logf, Config: func() (*hosts.Config, error) { return nil, errors.New("no hosts.toml") }}
	if err := broken.Refresh(); err == nil {
		t.Fatal("configuration failure ignored")
	}
	broken.Run(ctx)
}

func TestSlowHostReadKeepsObservationsRecordedMeanwhile(t *testing.T) {
	f := newObserverFixture(t, "devbox", "mac")
	held, _ := f.running(t, "devbox", "@1")
	pending := execute(t, f.svc, control.Request{Op: "create", Item: control.Item{Kind: "task", Title: "Task", Repo: f.repo.ID, Plan: f.plan.ID, Body: "## Outcome\nWorks"}})
	out, err := f.svc.Execute(control.Actor{Name: "operator", Role: "operator"}, control.Request{Op: "claim", Key: control.Secret(), ID: pending.ID, Body: "devbox", State: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	devbox := f.hosts["devbox"]
	devbox.set(hostops.Status{Windows: []hostops.Window{window(held, "@1", true, session.StateWorking), window(pending, "@2", true, session.StateWorking)}}, nil)
	gate := make(chan struct{})
	devbox.gate = gate
	if err = f.obs.Refresh(); err != nil {
		t.Fatal(err)
	}
	// While devbox is slow, its dispatch completes and a session starts on mac.
	if err = f.svc.SetAttempt(out.Attempt.ID, "running", "@2"); err != nil {
		t.Fatal(err)
	}
	later, onMac := f.running(t, "mac", "@1")
	st, _ := f.svc.Store.Snapshot()
	mac := reconcile.HostLoad{Name: "mac", Status: hostops.Status{Windows: []hostops.Window{window(later, "@1", true, session.StateWaiting)}}}
	f.obs.Record(st, reconcile.Fleet{Hosts: []reconcile.HostLoad{mac, {Name: "devbox", Unreachable: "busy"}}})
	t0 := f.clock.now()
	close(gate)
	f.obs.Settle()
	expectObservation(t, f.obs, onMac, session.StateWaiting, t0)
	expectObservation(t, f.obs, *out.Attempt, session.StateWorking, t0)
}
