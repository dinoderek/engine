package controlrun

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/session"
)

// endFirstAttempt runs the task's first attempt until its session dies and
// the task fails, leaves the task's window as alive says, as a window opened
// again by hand would be, and returns the task to todo for a retry.
func endFirstAttempt(t *testing.T, c *Coordinator, h *host, task control.Item, alive bool) control.Attempt {
	t.Helper()
	pass(t, c)
	pass(t, c)
	st, _ := c.Service.Store.Snapshot()
	first := st.Attempts[st.Items[task.ID].Attempt]
	h.status.Windows[0].Alive = false
	if err := c.ConfirmEnded(first.ID); err != nil {
		t.Fatal(err)
	}
	pass(t, c)
	st, _ = c.Service.Store.Snapshot()
	if st.Items[task.ID].State != "failed" || len(h.status.Windows) != 1 || len(h.released) != 0 {
		t.Fatalf("the dead attempt left %s, windows %v, released %v", st.Items[task.ID].State, h.status.Windows, h.released)
	}
	h.status.Windows[0].Alive = alive
	execute(t, c.Service, control.Request{Op: "state", ID: task.ID, Version: st.Items[task.ID].Version, State: "todo"})
	return first
}

func refusedNotices(t *testing.T, c *Coordinator, task control.Item) int {
	t.Helper()
	st, _ := c.Service.Store.Snapshot()
	n := 0
	for _, u := range st.Updates {
		if u.Item == task.ID && u.Recipient == "operator" && strings.Contains(u.Body, "refused to start this execution") {
			n++
		}
	}
	return n
}

func TestRetryReleasesTheDeadWindowOfTheEndedAttemptFirst(t *testing.T) {
	c, h, _, _, task := setup(t)
	first := endFirstAttempt(t, c, h, task, false)
	pass(t, c)
	pass(t, c)
	st, _ := c.Service.Store.Snapshot()
	retry := st.Attempts[st.Items[task.ID].Attempt]
	if retry.ID == first.ID || retry.State != "running" {
		t.Fatalf("the retry did not start: %+v", retry)
	}
	if want := []string{h.status.Windows[0].Name}; !slices.Equal(h.released, want) {
		t.Errorf("released %v, want the task's window once", h.released)
	}
	if !h.launches[retry.RunID].Spawn.Resume {
		t.Error("the retry did not dispatch as a resume")
	}
	if refusedNotices(t, c, task) != 0 {
		t.Error("a dispatch the host accepted posted a refusal")
	}
}

func TestRetryLeavesALiveWindowAlone(t *testing.T) {
	c, h, _, _, task := setup(t)
	first := endFirstAttempt(t, c, h, task, true)
	for range 3 {
		pass(t, c)
	}
	if len(h.released) != 0 || h.calls != 1 {
		t.Errorf("released %v and dispatched %d times past a live window, want neither", h.released, h.calls-1)
	}
	st, _ := c.Service.Store.Snapshot()
	if st.Items[task.ID].Attempt != first.ID {
		t.Error("a retry was dispatched past a live window")
	}
}

func TestARefusalWhoseReasonChangesIsNoticedOncePerExecution(t *testing.T) {
	c, h, _, _, task := setup(t)
	endFirstAttempt(t, c, h, task, false)
	for i, reason := range []string{"branch task/demo-2 already has a worktree with uncommitted changes", "the worktree is locked", "another reason"} {
		h.refuse = reason
		pass(t, c)
		if n := refusedNotices(t, c, task); n != 1 {
			t.Fatalf("pass %d: %d refusal notices, want 1", i, n)
		}
	}
	st, _ := c.Service.Store.Snapshot()
	if !strings.Contains(lastBody(st, task.ID), "uncommitted changes") {
		t.Errorf("the notice does not say why the host refused: %q", lastBody(st, task.ID))
	}
	h.refuse = ""
	pass(t, c)
	st, _ = c.Service.Store.Snapshot()
	if at := st.Attempts[st.Items[task.ID].Attempt]; at.State != "running" {
		t.Errorf("once the host accepts, the execution is %s, want running", at.State)
	}
}

func lastBody(st control.State, item string) string {
	body := ""
	for _, u := range st.Updates {
		if u.Item == item && strings.Contains(u.Body, "refused to start") {
			body = u.Body
		}
	}
	return body
}

func TestReleaseEndedActsOnlyOnAReadThatShowsTheWindowGone(t *testing.T) {
	st := control.State{
		Items: map[string]control.Item{"r1": {ID: "r1", Slug: "owner/demo"}, "t2": {ID: "t2", Repo: "r1"}},
		Attempts: map[string]control.Attempt{
			"a1": {ID: "a1", Item: "t2", Host: "devbox", State: "ended", Window: "@1", RunID: "run-1"},
			"a2": {ID: "a2", Item: "t2", Host: "devbox", State: "dispatching", RunID: "run-2"},
		},
	}
	dead := hostops.Window{Name: "demo-2", ID: "@1"}
	tests := []struct {
		name    string
		status  hostops.Status
		outage  bool
		current control.Attempt
		want    bool
	}{
		{name: "dead window", status: hostops.Status{Windows: []hostops.Window{dead}}, want: true},
		{name: "absent window", want: true},
		{name: "live window", status: hostops.Status{Windows: []hostops.Window{{Name: "demo-2", ID: "@1", Alive: true}}}},
		{name: "host unreadable", outage: true},
		{name: "run records unreadable", status: hostops.Status{Windows: []hostops.Window{dead}, RunsError: "run.json: bad"}},
		{name: "newest run is this execution's own", status: hostops.Status{Runs: []hostops.Run{{Name: "demo-2", Record: session.Record{RunID: "run-2"}}}}},
		{name: "earlier attempt on another host", status: hostops.Status{Windows: []hostops.Window{dead}}, current: control.Attempt{ID: "a2", Item: "t2", Host: "other", RunID: "run-2"}},
		{name: "no earlier attempt", status: hostops.Status{Windows: []hostops.Window{dead}}, current: control.Attempt{ID: "a1", Item: "t2", Host: "devbox", RunID: "run-1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &host{status: tt.status, outage: tt.outage}
			c := &Coordinator{Logf: t.Logf}
			current := tt.current
			if current.ID == "" {
				current = st.Attempts["a2"]
			}
			c.releaseEnded(st, st.Items["t2"], current, h)
			if got := slices.Equal(h.released, []string{"demo-2"}); got != tt.want {
				t.Errorf("released %v, want a release %v", h.released, tt.want)
			}
		})
	}
}

func TestReleaseEndedLogsAFailedRelease(t *testing.T) {
	st := control.State{
		Items:    map[string]control.Item{"r1": {ID: "r1", Slug: "owner/demo"}, "t2": {ID: "t2", Repo: "r1"}},
		Attempts: map[string]control.Attempt{"a1": {ID: "a1", Item: "t2", Host: "devbox", State: "ended", Window: "@1"}},
	}
	var logged []string
	c := &Coordinator{Logf: func(f string, a ...any) { logged = append(logged, f) }}
	c.releaseEnded(st, st.Items["t2"], control.Attempt{ID: "a2", Item: "t2", Host: "devbox"}, failingRelease{&host{}})
	if len(logged) != 1 || !strings.Contains(logged[0], "release the ended session") {
		t.Errorf("logged %v", logged)
	}
}

type failingRelease struct{ *host }

func (failingRelease) Release(string, bool) (hostops.Released, error) {
	return hostops.Released{}, errors.New("tmux is gone")
}
