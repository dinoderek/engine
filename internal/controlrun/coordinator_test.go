package controlrun

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/remote"
	"github.com/dinoderek/engine/internal/run"
	"github.com/dinoderek/engine/internal/session"
)

type host struct {
	status    hostops.Status
	outage    bool
	loseReply bool
	calls     int
	launches  map[string]hostops.ControlRequest
}

func (h *host) SyncRepo(string) (hostops.Synced, error)             { panic("legacy GitHub path used") }
func (h *host) Spawn(hostops.SpawnRequest) (hostops.Spawned, error) { panic("legacy GitHub path used") }
func (h *host) Collect(string) (hostops.Collected, error)           { return hostops.Collected{}, nil }
func (h *host) Check(string, io.Writer) (bool, error)               { return true, nil }
func (h *host) Release(name string, _ bool) (hostops.Released, error) {
	for i, w := range h.status.Windows {
		if w.Name == name {
			h.status.Windows = append(h.status.Windows[:i], h.status.Windows[i+1:]...)
			break
		}
	}
	return hostops.Released{}, nil
}
func (h *host) Status() (hostops.Status, error) {
	if h.outage {
		return hostops.Status{}, errors.New("host unavailable")
	}
	return h.status, nil
}
func (h *host) ControlSpawn(r hostops.ControlRequest) (hostops.Spawned, error) {
	h.calls++
	if prev, ok := h.launches[r.Spawn.RunID]; ok {
		if prev.Credential != r.Credential {
			panic("retry changed credential")
		}
		return hostops.Spawned{Window: "@1"}, nil
	}
	h.launches[r.Spawn.RunID] = r
	name := fmt.Sprintf("demo-%d", r.Spawn.Task)
	h.status.Windows = append(h.status.Windows, hostops.Window{Name: name, ID: "@1", Alive: true, State: session.StateWorking})
	h.status.Runs = append(h.status.Runs, hostops.Run{Name: name, Record: session.Record{RunID: r.Spawn.RunID, Name: name, Tmux: "engine:@1", Harness: r.Spawn.Harness, Spawned: time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)}})
	h.status.FreeSlots--
	if h.loseReply {
		h.loseReply = false
		return hostops.Spawned{}, errors.New("response lost")
	}
	return hostops.Spawned{Window: "@1"}, nil
}

func setup(t *testing.T) (*Coordinator, *host, control.Item, control.Item, control.Item) {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 absent")
	}
	store, err := control.Open(filepath.Join(t.TempDir(), "db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	svc := control.Service{Store: store, Now: func() time.Time { return time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC) }}
	repo := execute(t, svc, control.Request{Op: "create", Item: control.Item{Kind: "repo", Title: "Demo", Slug: "owner/demo", Remote: "ssh://example.test/demo"}})
	plan := execute(t, svc, control.Request{Op: "create", Item: control.Item{Kind: "plan", Title: "Feature", Repo: repo.ID, Body: "Requirements"}})
	task := execute(t, svc, control.Request{Op: "create", Item: control.Item{Kind: "task", Title: "Task", Repo: repo.ID, Plan: plan.ID, Body: "## Outcome\nWorks\n## Tests\nProve it"}})
	plan = execute(t, svc, control.Request{Op: "approve", ID: plan.ID, Version: plan.Version})
	plan = execute(t, svc, control.Request{Op: "run", ID: plan.ID, Version: plan.Version})
	h := &host{status: hostops.Status{FreeSlots: 2, FreeHeavySlots: 2}, launches: map[string]hostops.ControlRequest{}}
	cfg := &hosts.Config{DefaultHarness: "claude", HarnessRatio: map[string]int{"claude": 1},
		Harnesses: map[string]hosts.Harness{"claude": {MaxSessions: 2, Tiers: map[hosts.Tier]hosts.TierModel{
			hosts.Standard: {Model: "executor", Effort: "high"}, hosts.Deep: {Model: "reviewer", Effort: "high"}}}},
		Hosts: map[string]hosts.Host{"devbox": {Name: "devbox", Harnesses: []string{"claude"}, MaxSessions: 2}}}
	c := &Coordinator{Service: svc, URL: "http://control.test", Config: cfg, Host: cfg.Hosts["devbox"], Client: func(hosts.Host) remote.Client { return h }, Logf: t.Logf}
	return c, h, repo, plan, task
}

func execute(t *testing.T, s control.Service, r control.Request) control.Item {
	t.Helper()
	r.Key = control.Secret()
	out, err := s.Execute(control.Actor{Name: "operator", Role: "operator"}, r)
	if err != nil {
		t.Fatal(r.Op, err)
	}
	if out.Item == nil {
		return control.Item{}
	}
	return *out.Item
}
func pass(t *testing.T, c *Coordinator) {
	t.Helper()
	if err := c.Pass(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCoordinatorDispatchesWithoutGitHubAndResumesAfterRestart(t *testing.T) {
	c, h, _, plan, task := setup(t)
	pass(t, c)
	pass(t, c)
	if len(h.launches) != 1 {
		t.Fatalf("launches: %v", h.launches)
	}
	st, _ := c.Service.Store.Snapshot()
	at := st.Attempts[st.Items[task.ID].Attempt]
	if at.State != "running" || st.Items[task.ID].State != "running" {
		t.Fatal("claim not atomic")
	}
	// A fresh coordinator over the same durable state adopts the live session.
	resumed := *c
	pass(t, &resumed)
	if h.calls != 1 {
		t.Fatal("restart dispatched again")
	}
	if err := c.ConfirmEnded(at.ID); err == nil {
		t.Fatal("live execution considered ended")
	}
	h.outage = true
	pass(t, &resumed)
	st, _ = c.Service.Store.Snapshot()
	if st.Items[task.ID].State != "running" || len(st.Attempts) != 1 {
		t.Fatal("outage changed ownership")
	}
	if err := c.ConfirmEnded(at.ID); err == nil {
		t.Fatal("outage considered dead")
	}
	h.outage = false
	current := st.Items[task.ID]
	execute(t, c.Service, control.Request{Op: "state", ID: task.ID, Version: current.Version, State: "done"})
	pass(t, c)
	st, _ = c.Service.Store.Snapshot()
	if st.Items[plan.ID].State != "done" {
		t.Fatal(st.Items[plan.ID])
	}
}

func TestUncertainDispatchIsAdoptedWithoutDuplicatingIt(t *testing.T) {
	c, h, _, _, task := setup(t)
	h.loseReply = true
	pass(t, c)
	pass(t, c)
	st, _ := c.Service.Store.Snapshot()
	id := st.Items[task.ID].Attempt
	if st.Attempts[id].State != "dispatching" {
		t.Fatal("uncertain dispatch was cleared")
	}
	pass(t, c)
	st, _ = c.Service.Store.Snapshot()
	if h.calls != 1 || len(h.launches) != 1 || st.Attempts[id].State != "running" {
		t.Fatal("dispatch not adopted")
	}
}

func TestInterruptedBeforeDispatchReusesPersistedIdentity(t *testing.T) {
	c, h, _, _, task := setup(t)
	pass(t, c)
	out, err := c.Service.Dispatch(task, "devbox", "claude", "interrupted")
	if err != nil {
		t.Fatal(err)
	}
	pass(t, c)
	st, _ := c.Service.Store.Snapshot()
	if h.calls != 1 || len(st.Attempts) != 1 || st.Items[task.ID].Attempt != out.Attempt.ID {
		t.Fatal("interrupted dispatch duplicated")
	}
	if st.Items[task.ID].State != "running" || st.Attempts[out.Attempt.ID].State != "running" {
		t.Fatal("recovered dispatch was treated as dead")
	}
}

func TestDeadExecutionFailsAndKeepsItsMergeLock(t *testing.T) {
	c, h, repo, _, task := setup(t)
	pass(t, c)
	pass(t, c)
	st, _ := c.Service.Store.Snapshot()
	current := st.Items[task.ID]
	at := st.Attempts[current.Attempt]
	_, err := c.Service.Execute(control.Actor{Name: "operator", Role: "operator"}, control.Request{Op: "lock-acquire", Attempt: at.ID, Key: "lock"})
	if err != nil {
		t.Fatal(err)
	}
	h.status.Windows[0].Alive = false
	if err = c.ConfirmEnded(at.ID); err != nil {
		t.Fatal(err)
	}
	pass(t, c)
	st, _ = c.Service.Store.Snapshot()
	if st.Items[task.ID].State != "failed" || st.Attempts[at.ID].Reason != control.EndFailed || st.Locks[repo.ID].Attempt != at.ID {
		t.Fatal("failure dropped ownership evidence")
	}
}

func TestPlannerDispatchAndInvalidPlanBlock(t *testing.T) {
	c, h, _, plan, task := setup(t)
	if err := c.Service.Store.Transaction(func(st *control.State) error { delete(st.Items, task.ID); return nil }); err != nil {
		t.Fatal(err)
	}
	pass(t, c)
	for _, req := range h.launches {
		if req.Spawn.Tier != "deep" || req.Spawn.Task != control.Number(plan.ID) {
			t.Fatal(req)
		}
	}
	// The planner's session remains the unique holder.
	pass(t, c)
	if h.calls != 1 {
		t.Fatal("planner duplicated")
	}
}

func TestCoordinatorCancellationAndObservation(t *testing.T) {
	c, h, _, _, _ := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Run(ctx)
	if err := c.Pass(ctx); err == nil {
		t.Fatal("pass ignored cancellation")
	}
	h.status.RunsError = "unreadable"
	s := observe(control.Attempt{Host: "devbox", State: "dispatching"}, run.Fleet{Hosts: []run.HostLoad{{Name: "devbox", Status: h.status}}}, "owner/demo", 1)
	if !s.Unknown {
		t.Fatal("unreadable records treated as dead")
	}
}
