package controlrun

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/reconcile"
	"github.com/dinoderek/engine/internal/remote"
	"github.com/dinoderek/engine/internal/session"
)

// flaky is a host whose next fails releases fail.
type flaky struct {
	*host
	fails int
}

func (f *flaky) Release(name string, keep bool) (hostops.Released, error) {
	if f.fails > 0 {
		f.fails--
		return hostops.Released{}, errors.New("tmux is busy")
	}
	return f.host.Release(name, keep)
}

// Once its plan is done, every waiting session is released in the pass,
// but coordination never stops while a window it failed to release is
// open: a later pass releases it, then stops.
func TestADoneRootNeverStopsAfterAFailedRelease(t *testing.T) {
	c, h, repo, plan, task := setup(t)
	second := execute(t, c.Service, control.Request{Op: "create", Item: control.Item{Kind: "task", Title: "Acceptance", Repo: repo.ID, Plan: plan.ID, Body: task.Body, Depends: []string{task.ID}}})
	pass(t, c)
	pass(t, c)
	name := fmt.Sprintf("demo-%d", control.Number(second.ID))
	if err := c.Service.Store.Transaction(func(st *control.State) error {
		first := st.Attempts[st.Items[task.ID].Attempt]
		other := first
		other.ID, other.Item, other.Window, other.RunID = "a900", second.ID, "@2", "demo-a900"
		st.Attempts[other.ID] = other
		for id, at := range map[string]string{task.ID: first.ID, second.ID: other.ID, plan.ID: ""} {
			item := st.Items[id]
			item.State, item.Version = "done", item.Version+1
			if at != "" {
				item.Attempt = at
			}
			st.Items[id] = item
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h.status.Windows[0].State = session.StateWaiting
	h.status.Windows = append(h.status.Windows, hostops.Window{Name: name, ID: "@2", Alive: true, State: session.StateWaiting})
	f := &flaky{host: h, fails: 1}
	c.Client = func(hosts.Host) remote.Client { return f }
	pass(t, c)
	st, _ := c.Service.Store.Snapshot()
	if !st.Items[plan.ID].Coordinating || len(h.status.Windows) != 1 || st.Attempts["a900"].State != "ended" {
		t.Fatalf("coordinating %v, windows %v, second attempt %s", st.Items[plan.ID].Coordinating, h.status.Windows, st.Attempts["a900"].State)
	}
	pass(t, c)
	st, _ = c.Service.Store.Snapshot()
	if st.Items[plan.ID].Coordinating || len(h.status.Windows) != 0 {
		t.Fatalf("coordinating %v, windows %v after the retry", st.Items[plan.ID].Coordinating, h.status.Windows)
	}
}

// The merge rereads the server just before the host call: a lock released
// after the pass decided to merge refuses the request instead.
func TestAMergeRereadsTheServerBeforeTheHostCall(t *testing.T) {
	c, m, task, _ := requested(t, true, reply(hostops.MergeDone, "merged"))
	st, _ := c.Service.Store.Snapshot()
	acts := reconcile.Decide(c.view(st, task.Plan, reconcile.Fleet{}))
	if len(acts) == 0 || acts[0].Kind != reconcile.Merge || acts[0].Refuse != "" {
		t.Fatalf("actions %+v", acts)
	}
	if err := c.Service.Store.Transaction(func(s *control.State) error { delete(s.Locks, task.Repo); return nil }); err != nil {
		t.Fatal(err)
	}
	if err := c.merge(acts[0], c.Config); err != nil {
		t.Fatal(err)
	}
	if u, ok := lastUpdate(t, c, task.ID, control.MergeRefused); !ok || !strings.Contains(u.Body, "merge lock") || len(m.requests) != 0 {
		t.Fatalf("refusal %+v, merges %v", u, m.requests)
	}
}
