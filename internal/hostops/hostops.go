// Package hostops is the work one host does on itself for a caller on any
// host: `engine host <op> --json`. Each op does one job and returns one
// reply, and the command prints it as one JSON object inside the envelope.
// The ops never read the tracker: the caller has already decided, and passes
// what it decided, such as the model and the kickoff.
package hostops

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/session"
)

// Protocol is the version of the ops' wire format. A caller refuses a reply
// that carries another.
const Protocol = 5

// The executor's modes. The kickoff names one, and a hands-off session is
// marked unattended.
const (
	HandsOff = "hands-off"
	Gated    = "gated"
)

// Deps are the outside world: the CLIs, the home directory, the environment
// and the clock. Tests replace each one.
type Deps struct {
	// Run runs git and gh for engine's clones and worktrees.
	Run session.RunFunc
	// Probe reads a repo's own `dev` usage under a deadline; nil uses Run.
	Probe session.RunFunc
	// Exec runs the host's own CLIs, such as the harness binaries.
	Exec session.Runner
	Tmux *session.Tmux
	Home string
	// Getenv reads the caller's environment.
	Getenv func(string) string
	Now    func() time.Time
	// BaseURL is where repos are cloned from; GitHub when empty.
	BaseURL string
	// Sleep waits between the check's polls; nil is time.Sleep.
	Sleep func(time.Duration)
	// JoinRemote lets a spawn and the check join engine's remote-control
	// daemon when it runs with remote control on. Off, a session starts
	// embedded and nothing asks the daemon its state. The commands set it.
	JoinRemote bool
	// Harnesses are the harness adapters, built from the same Exec, Home,
	// Getenv and Now.
	Harnesses harness.Adapters
	// CodexLabel titles a thread and files it under a project, through
	// engine's daemon. Nil uses `codex app-server proxy`.
	CodexLabel func(context.Context, string, string, CodexLabelRequest) error
}

// RefusedError is an op that refused before it changed anything.
type RefusedError struct{ Reason string }

func (e *RefusedError) Error() string { return e.Reason + "; nothing was changed" }

func refuse(format string, args ...any) error {
	return &RefusedError{Reason: fmt.Sprintf(format, args...)}
}

// nameRe is a session name, as tmux takes it for a window.
var nameRe = regexp.MustCompile(`^[A-Za-z0-9_.][A-Za-z0-9_.-]*$`)

// openHost reads hosts.toml and the host file, and returns this host.
func openHost(home string) (*hosts.Config, hosts.Host, error) {
	cfg, host, err := hosts.Open(hosts.ConfigDir(home))
	if err != nil {
		return nil, hosts.Host{}, refuse("%v", err)
	}
	return cfg, host, nil
}

// checkHarness refuses a host that does not run the harness, or whose
// harness binary is not an executable file. It returns the binary's path.
func checkHarness(host hosts.Host, harness, home string) (string, error) {
	if !host.Runs(harness) {
		return "", refuse("host %s does not run %s", host.Name, harness)
	}
	bin, err := host.BinPath(harness, home)
	if err != nil {
		return "", refuse("%v", err)
	}
	if info, err := os.Stat(bin); err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		return "", refuse("%s is not an executable %s binary (hosts.toml, host %s, bin)", bin, harness, host.Name)
	}
	return bin, nil
}

// Synced is sync-repo's reply.
type Synced struct {
	Clone         string `json:"clone"`
	DefaultBranch string `json:"default_branch"`
}

// SyncRepo makes sure engine's clone of repo (owner/name) exists, fetches it
// and leaves it at the remote's default branch.
func SyncRepo(d Deps, repo string) (Synced, error) {
	r, err := session.ParseRepo(repo)
	if err != nil {
		return Synced{}, refuse("%v", err)
	}
	clone, err := session.Repos{Run: d.Run, Home: d.Home, BaseURL: d.BaseURL}.Sync(r)
	if err != nil {
		return Synced{}, err
	}
	return Synced{Clone: clone.Path, DefaultBranch: clone.Default}, nil
}
