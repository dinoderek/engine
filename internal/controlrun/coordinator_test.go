package controlrun

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/reconcile"
	"github.com/dinoderek/engine/internal/remote"
	"github.com/dinoderek/engine/internal/session"
)

type host struct {
	status    hostops.Status
	outage    bool
	loseReply bool
	calls     int
	launches  map[string]hostops.ControlRequest
	released  []string
	outcomes  []hostops.Outcome
	// note is what each release replies with in its note.
	note string
	// refuse, when set, is the reason the host refuses a new dispatch.
	refuse string
	// unresolved makes a dispatch fail with no reply and no effect.
	unresolved bool
}

func (h *host) SyncRepo(string) (hostops.Synced, error)             { panic("legacy GitHub path used") }
func (h *host) Spawn(hostops.SpawnRequest) (hostops.Spawned, error) { panic("legacy GitHub path used") }
func (h *host) Collect(string) (hostops.Collected, error)           { return hostops.Collected{}, nil }
func (h *host) Check(string, io.Writer) (bool, error)               { return true, nil }
func (h *host) Release(name string, outcome hostops.Outcome) (hostops.Released, error) {
	h.released = append(h.released, name)
	h.outcomes = append(h.outcomes, outcome)
	for i, w := range h.status.Windows {
		if w.Name == name {
			h.status.Windows = append(h.status.Windows[:i], h.status.Windows[i+1:]...)
			break
		}
	}
	return hostops.Released{Name: name, Note: h.note}, nil
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
	name := fmt.Sprintf("demo-%d", r.Spawn.Task)
	if h.unresolved {
		return hostops.Spawned{}, errors.New("connection closed")
	}
	if h.refuse != "" {
		return hostops.Spawned{}, &hostops.RefusedError{Reason: h.refuse}
	}
	// As the host does, a window name in use, live or dead, is refused.
	for _, w := range h.status.Windows {
		if w.Name == name {
			return hostops.Spawned{}, &hostops.RefusedError{Reason: "the tmux session engine already has a window " + name}
		}
	}
	h.launches[r.Spawn.RunID] = r
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

// The planner runs with the plan's harness, else the default, and a harness
// the server's host does not run is refused with a notice and no execution.
func TestPlannerHarnessIsThePlansElseTheDefault(t *testing.T) {
	for _, tt := range []struct {
		name, harness, want string
		hostRuns            []string
	}{
		{"default", "", "claude", []string{"claude", "codex"}},
		{"plan's", "codex", "codex", []string{"claude", "codex"}},
		{"undefined", "gemini", "", []string{"claude", "codex"}},
		{"not on the host", "codex", "", []string{"claude"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c, h, _, plan, task := setup(t)
			c.Config.Harnesses["codex"] = c.Config.Harnesses["claude"]
			c.Config.Hosts["devbox"] = hosts.Host{Name: "devbox", Harnesses: tt.hostRuns, MaxSessions: 2}
			if err := c.Service.Store.Transaction(func(st *control.State) error {
				delete(st.Items, task.ID)
				p := st.Items[plan.ID]
				p.Harness = tt.harness
				st.Items[plan.ID] = p
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			pass(t, c)
			st, _ := c.Service.Store.Snapshot()
			if tt.want == "" {
				if h.calls != 0 || st.Items[plan.ID].Attempt != "" || len(st.Updates) == 0 ||
					!strings.Contains(st.Updates[len(st.Updates)-1].Body, "planner harness "+tt.harness+" is not one host devbox runs") {
					t.Fatalf("calls %d attempt %q updates %+v", h.calls, st.Items[plan.ID].Attempt, st.Updates)
				}
				return
			}
			if h.calls != 1 {
				t.Fatalf("calls %d", h.calls)
			}
			for _, req := range h.launches {
				if req.Spawn.Harness != tt.want || st.Attempts[st.Items[plan.ID].Attempt].Harness != tt.want {
					t.Fatal(req.Spawn.Harness)
				}
			}
		})
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
	s := observe(control.Attempt{Host: "devbox", State: "dispatching"}, reconcile.Fleet{Hosts: []reconcile.HostLoad{{Name: "devbox", Status: h.status}}}, "owner/demo", 1)
	if !s.Unknown {
		t.Fatal("unreadable records treated as dead")
	}
}
