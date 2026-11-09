package harnesstest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/session"
)

// Fake is an adapter that records what the core asks of it and answers what
// the test set. Its launch line is " fake-args -- <kickoff>".
type Fake struct {
	// Called is the harness's name.
	Called string
	// Snapshot is what Observe returns; Observed records the runs it got.
	Snapshot harness.Snapshot
	Observed []map[string]session.Run
	// Joined and Warning are Join's answer.
	Joined  bool
	Warning string
	// TrustErr and ReleaseErr fail Trust and Release.
	TrustErr, ReleaseErr error
	// Hooked is HookedState's answer.
	Hooked bool
	// Policy, Finds and Own are its hooks' Unattended, FindsRun and Entries.
	Policy harness.PermissionPolicy
	Finds  bool
	Own    map[string]harness.Entry
	// Detach, when Signalled is given an event of this kind, is the entry it
	// starts in the background, with the run directory and the session ID.
	Detach harness.EventKind

	mu        sync.Mutex
	calls     []string
	signalled []harness.Event
	runs      []harness.HookRun
}

// Signalled are the events Signalled got so far, and the run each was for.
func (f *Fake) Signalled() ([]harness.Event, []harness.HookRun) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]harness.Event(nil), f.signalled...), append([]harness.HookRun(nil), f.runs...)
}

// Calls are the launcher calls so far, one "<op> <session> <arg>" each.
func (f *Fake) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *Fake) record(parts ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, strings.Join(parts, " "))
}

func (f *Fake) Name() string { return f.Called }

func (f *Fake) Session(name string) harness.Launcher { return fakeLauncher{f, name} }

func (f *Fake) Observe(_ context.Context, _ hosts.Host, runs map[string]session.Run) harness.Snapshot {
	f.mu.Lock()
	f.Observed = append(f.Observed, runs)
	f.mu.Unlock()
	return f.Snapshot
}

func (f *Fake) CheckKickoff(marker string) string { return "fake check " + marker }

func (f *Fake) HookedState() bool { return f.Hooked }

type fakeLauncher struct {
	f    *Fake
	name string
}

func (l fakeLauncher) Join(bin, _ string) (bool, string) {
	l.f.record("join", l.name, bin)
	return l.f.Joined, l.f.Warning
}

func (l fakeLauncher) Trust(dir, _ string, _ bool) error {
	l.f.record("trust", l.name, dir)
	return l.f.TrustErr
}

func (l fakeLauncher) Release(dir string) error {
	l.f.record("release", l.name, dir)
	return l.f.ReleaseErr
}

func (l fakeLauncher) Env(bool) string { return "export FAKE_HOME=/fake && " }

func (l fakeLauncher) Args(s harness.Launch) string {
	return " fake-args -- " + hosts.ShellWord(s.Kickoff)
}

func (f *Fake) Hooks() harness.Hooks { return fakeHooks{f} }

// fakeHooks reads an event as {"kind": …, "name": …, "session_id": …,
// "cwd": …, "command": …}, and denies with {"denied": <message>}.
type fakeHooks struct{ f *Fake }

func (h fakeHooks) Event(raw []byte) (harness.Event, error) {
	var e struct {
		Kind      harness.EventKind `json:"kind"`
		Name      string            `json:"name"`
		SessionID string            `json:"session_id"`
		Cwd       string            `json:"cwd"`
		Command   string            `json:"command"`
	}
	if err := json.Unmarshal(raw, &e); err != nil {
		return harness.Event{}, err
	}
	return harness.Event{Kind: e.Kind, Name: e.Name, SessionID: e.SessionID, Cwd: e.Cwd, Command: e.Command}, nil
}

func (fakeHooks) Deny(w io.Writer, message string) error {
	return json.NewEncoder(w).Encode(map[string]string{"denied": message})
}

func (h fakeHooks) Unattended() harness.PermissionPolicy { return h.f.Policy }

func (h fakeHooks) FindsRun() bool { return h.f.Finds }

func (h fakeHooks) Signalled(c harness.HookContext, ev harness.Event) {
	h.f.mu.Lock()
	h.f.signalled = append(h.f.signalled, ev)
	h.f.runs = append(h.f.runs, c.Run)
	h.f.mu.Unlock()
	if h.f.Detach != "" && ev.Kind == h.f.Detach && c.Detach != nil {
		if err := c.Detach("fake-entry", c.Run.Dir, ev.SessionID); err != nil {
			fmt.Fprintln(c.Err, "fake detach:", err)
		}
	}
}

func (h fakeHooks) Entries() map[string]harness.Entry { return h.f.Own }

var _ harness.Adapter = (*Fake)(nil)
