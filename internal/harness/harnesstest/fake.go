package harnesstest

import (
	"context"
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

	mu    sync.Mutex
	calls []string
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

var _ harness.Adapter = (*Fake)(nil)
