// Package remote reaches a host's ops (internal/hostops), whichever host it
// is. A Client is one host. Local is this one, called in-process, so a
// session started here runs the same code a remote host runs for its caller.
package remote

import (
	"io"

	"github.com/dinoderek/engine/internal/hostops"
)

// Client runs the ops on one host.
type Client interface {
	SyncRepo(repo string) (hostops.Synced, error)
	Spawn(r hostops.SpawnRequest) (hostops.Spawned, error)
	Status() (hostops.Status, error)
	Collect(runID string) (hostops.Collected, error)
	Release(name string, failed bool) (hostops.Released, error)
	// Check runs the preflight check on the host, prints its report to out,
	// and reports whether an unattended session there would run without a
	// prompt.
	Check(out io.Writer) (bool, error)
}

// Local is this host.
type Local struct{ Deps hostops.Deps }

var _ Client = Local{}

func (l Local) SyncRepo(repo string) (hostops.Synced, error) { return hostops.SyncRepo(l.Deps, repo) }

func (l Local) Spawn(r hostops.SpawnRequest) (hostops.Spawned, error) {
	return hostops.Spawn(l.Deps, r)
}

func (l Local) Status() (hostops.Status, error) { return hostops.ReadStatus(l.Deps) }

func (l Local) Collect(runID string) (hostops.Collected, error) {
	return hostops.Collect(l.Deps, runID)
}

func (l Local) Release(name string, failed bool) (hostops.Released, error) {
	return hostops.Release(l.Deps, name, failed)
}

func (l Local) Check(out io.Writer) (bool, error) {
	return hostops.RunCheck(l.Deps, hostops.Claude, out)
}
