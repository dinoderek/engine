package controlrun

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/remote"
)

// readyHost is a host whose ready op answers with probe, or fails with err,
// once gate, when set, is closed, and records each request.
type readyHost struct {
	*host
	mu    sync.Mutex
	probe hostops.Readiness
	err   error
	gate  chan struct{}
	asked []hostops.ReadyRequest
}

func (h *readyHost) Ready(r hostops.ReadyRequest) (hostops.Readiness, error) {
	if h.gate != nil {
		<-h.gate
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.asked = append(h.asked, r)
	p := h.probe
	p.Repo = r.Repo
	return p, h.err
}

// readySetup is setup's coordinator with readiness probes, on a clock the
// test moves.
func readySetup(t *testing.T) (*Coordinator, *readyHost, control.Item, control.Item, *time.Time) {
	t.Helper()
	c, h, repo, _, task := setup(t)
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	rh := &readyHost{host: h, probe: hostops.Readiness{Repo: "owner/demo", Head: strings.Repeat("a", 40), Checks: []hostops.ReadyCheck{{Name: hostops.GHCheck, OK: true}}}}
	c.Client = func(hosts.Host) remote.Client { return rh }
	c.Ready = &Readiness{Client: c.Client, Now: func() time.Time { return now }, Logf: t.Logf}
	return c, rh, repo, task, &now
}

// settled is a pass that then waits for the probes it started.
func settled(t *testing.T, c *Coordinator) {
	t.Helper()
	pass(t, c)
	c.Ready.Settle()
}

// notes are the operator notices whose body contains text.
func notes(t *testing.T, c *Coordinator, text string) []control.Update {
	t.Helper()
	st, err := c.Service.Store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	var out []control.Update
	for _, u := range st.Updates {
		if u.Kind == "say" && u.Recipient == "operator" && strings.Contains(u.Body, text) {
			out = append(out, u)
		}
	}
	return out
}

// Nothing is placed until the host's probe answers; then a ready host takes
// the task.
func TestCoordinatorWaitsForTheProbeThenPlaces(t *testing.T) {
	c, h, repo, _, _ := readySetup(t)
	h.gate = make(chan struct{})
	pass(t, c)
	pass(t, c)
	if len(h.launches) != 0 {
		t.Fatalf("dispatched before the probe answered: %v", h.launches)
	}
	close(h.gate)
	c.Ready.Settle()
	if len(h.asked) != 1 || h.asked[0].Repo != "owner/demo" || h.asked[0].Remote != repo.Remote || h.asked[0].Fresh {
		t.Fatalf("asked %+v", h.asked)
	}
	pass(t, c)
	if len(h.launches) != 1 {
		t.Fatalf("launches %v", h.launches)
	}
	if got := notes(t, c, "placement"); len(got) != 0 {
		t.Errorf("a pending probe sent a notice: %v", got)
	}
}

// A refused gh sign-in keeps the task off the host and posts one note naming
// the host, the check and its output; asking again with the same failure
// posts nothing, and a new failure posts its own.
func TestCoordinatorNotesEachDistinctReadinessFailureOnce(t *testing.T) {
	c, h, repo, _, now := readySetup(t)
	h.probe.Checks = []hostops.ReadyCheck{{Name: hostops.GHCheck, Output: "X Failed to log in to github.com account octo (keyring)"}}
	settled(t, c)
	settled(t, c)
	if len(h.launches) != 0 {
		t.Fatalf("an unready host took the task: %v", h.launches)
	}
	got := notes(t, c, "Host devbox is unready for owner/demo")
	if len(got) != 1 || got[0].Item != repo.ID || !strings.Contains(got[0].Body, "`gh auth status` failed on the default branch's head aaaaaaaaaaaa") ||
		!strings.Contains(got[0].Body, "(keyring)") || !strings.Contains(got[0].Body, "engine setup --host devbox") {
		t.Fatalf("notes %+v", got)
	}
	*now = now.Add(ReadyEvery)
	settled(t, c)
	pass(t, c)
	if same := notes(t, c, "Host devbox is unready for"); len(h.asked) != 2 || len(same) != 1 {
		t.Errorf("asked %d times; the same failure noted %d times", len(h.asked), len(same))
	}
	h.probe.Checks = []hostops.ReadyCheck{{Name: hostops.GHCheck, OK: true}, {Name: hostops.DoctorCheck, Output: "go: missing"}}
	*now = now.Add(ReadyEvery)
	settled(t, c)
	pass(t, c)
	if got := notes(t, c, "`./dev doctor` failed"); len(got) != 1 || !strings.Contains(got[0].Body, "go: missing") {
		t.Errorf("doctor's note %+v", got)
	}
	h.probe.Checks = []hostops.ReadyCheck{{Name: hostops.GHCheck, OK: true}}
	*now = now.Add(ReadyEvery)
	settled(t, c)
	pass(t, c)
	if len(h.launches) != 1 {
		t.Errorf("a host ready again took nothing: %v", h.launches)
	}
}

// A probe that cannot run keeps the host unready, with a note of its own; an
// unreachable host is left to its status and posts nothing.
func TestCoordinatorNotesAProbeThatCannotRun(t *testing.T) {
	c, h, _, _, now := readySetup(t)
	h.err = &remote.UnreachableError{Host: "devbox", Address: "devbox", Detail: "timeout"}
	settled(t, c)
	if got := notes(t, c, "readiness probe"); len(got) != 0 {
		t.Fatalf("an unreachable host was noted: %v", got)
	}
	h.err = errors.New("host devbox: unknown op \"ready\"")
	*now = now.Add(ReadyEvery)
	settled(t, c)
	settled(t, c)
	if got := notes(t, c, "The readiness probe of owner/demo could not run on devbox"); len(got) != 1 || !strings.Contains(got[0].Body, "unknown op") {
		t.Fatalf("notes %+v", got)
	}
	if len(h.launches) != 0 {
		t.Errorf("a host whose probe cannot run took the task: %v", h.launches)
	}
}

// A host whose tmux server's directory is gone gets one note, naming the
// restart command.
func TestCoordinatorNotesADeadTmuxDirectoryOnce(t *testing.T) {
	c, h, _, _, _ := readySetup(t)
	h.status.TmuxCwdGone = &hostops.TmuxCwdGone{Pid: 7, Dir: "/gone", Reason: "tmux server 7 runs in /gone; restart it with `tmux kill-server`"}
	settled(t, c)
	settled(t, c)
	if got := notes(t, c, "Host devbox is unready: tmux server 7"); len(got) != 1 || !strings.Contains(got[0].Body, "tmux kill-server") {
		t.Errorf("notes %+v", got)
	}
}

// Two repos failing the same way on one host each get their own note.
func TestCoordinatorNotesTheSameFailureOncePerRepo(t *testing.T) {
	c, h, repo, _, _ := readySetup(t)
	other := execute(t, c.Service, control.Request{Op: "create", Item: control.Item{Kind: "repo", Title: "Other", Slug: "owner/other", Remote: "ssh://example.test/other"}})
	plan := execute(t, c.Service, control.Request{Op: "create", Item: control.Item{Kind: "plan", Title: "Other feature", Repo: other.ID, Body: "Requirements"}})
	execute(t, c.Service, control.Request{Op: "create", Item: control.Item{Kind: "task", Title: "Task", Repo: other.ID, Plan: plan.ID, Body: "## Outcome\nWorks\n## Tests\nProve it"}})
	plan = execute(t, c.Service, control.Request{Op: "approve", ID: plan.ID, Version: plan.Version})
	execute(t, c.Service, control.Request{Op: "run", ID: plan.ID, Version: plan.Version})
	h.probe.Checks = []hostops.ReadyCheck{{Name: hostops.GHCheck, Output: "X Failed to log in to github.com account octo (keyring)"}}
	settled(t, c)
	settled(t, c)
	got := notes(t, c, "Host devbox is unready for")
	if len(got) != 2 || got[0].Item == got[1].Item || (got[0].Item != repo.ID && got[1].Item != repo.ID) {
		t.Errorf("notes %+v", got)
	}
}

// A restarted coordinator that sees the same failure on a new head finds the
// note's key already posted: nothing is posted again, and nothing is logged.
func TestCoordinatorAfterARestartDoesNotRepostANote(t *testing.T) {
	c, h, _, _, now := readySetup(t)
	h.probe.Checks = []hostops.ReadyCheck{{Name: hostops.GHCheck, Output: "X Failed to log in"}}
	settled(t, c)
	pass(t, c)
	var logged []string
	c.Logf = func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) }
	c.Ready = &Readiness{Client: c.Client, Now: func() time.Time { return *now }, Logf: c.Logf}
	h.probe.Head = strings.Repeat("b", 40)
	settled(t, c)
	pass(t, c)
	pass(t, c)
	if got := notes(t, c, "Host devbox is unready for"); len(got) != 1 {
		t.Errorf("notes %+v", got)
	}
	for _, l := range logged {
		if strings.Contains(l, "readiness note") {
			t.Errorf("logged %q", l)
		}
	}
}
