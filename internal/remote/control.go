package remote

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/session"
)

// ControlSpawn uses the host's JSON stdin channel to transfer execution credentials.
func ControlSpawn(client Client, r hostops.ControlRequest) (hostops.Spawned, error) {
	switch c := client.(type) {
	case Local:
		return hostops.ControlSpawn(c.Deps, r)
	case SSH:
		b, err := json.Marshal(r)
		if err != nil {
			return hostops.Spawned{}, err
		}
		var reply hostops.Spawned
		err = c.call("control-spawn", nil, string(b), &reply)
		return reply, err
	default:
		if c, ok := client.(interface {
			ControlSpawn(hostops.ControlRequest) (hostops.Spawned, error)
		}); ok {
			return c.ControlSpawn(r)
		}
		return hostops.Spawned{}, fmt.Errorf("host client does not support control dispatch")
	}
}

// ControlMerge has the task's host merge its PR with that host's gh, so the
// merge runs as the coordinator's code, never as an agent's tool call.
func ControlMerge(client Client, r hostops.MergeRequest) (hostops.Merged, error) {
	switch c := client.(type) {
	case Local:
		return hostops.Merge(c.Deps, r)
	case SSH:
		var reply hostops.Merged
		args := []string{"--repo", r.Repo, "--pr", strconv.Itoa(r.PR), "--head", r.Head, "--method", r.Method}
		return reply, c.call("merge", args, "", &reply)
	default:
		if c, ok := client.(interface {
			ControlMerge(hostops.MergeRequest) (hostops.Merged, error)
		}); ok {
			return c.ControlMerge(r)
		}
		return hostops.Merged{}, fmt.Errorf("host client does not support merges")
	}
}

// Ready asks the host whether the repo's sessions can work there (the ready
// op). A client without the op says so in its error.
func Ready(client Client, r hostops.ReadyRequest) (hostops.Readiness, error) {
	switch c := client.(type) {
	case Local:
		return hostops.Ready(c.Deps, r)
	case SSH:
		args := []string{"--repo", r.Repo}
		if r.Remote != "" {
			args = append(args, "--remote", r.Remote)
		}
		if r.Fresh {
			args = append(args, "--fresh")
		}
		var reply hostops.Readiness
		return reply, c.call("ready", args, "", &reply)
	default:
		if c, ok := client.(interface {
			Ready(hostops.ReadyRequest) (hostops.Readiness, error)
		}); ok {
			return c.Ready(r)
		}
		return hostops.Readiness{}, fmt.Errorf("host client does not support the readiness probe")
	}
}

// RunReady probes the repo on the host afresh, prints the report to out, and
// reports whether every check passed.
func RunReady(client Client, repo string, out io.Writer) (bool, error) {
	r, err := Ready(client, hostops.ReadyRequest{Repo: repo, Fresh: true})
	if err != nil {
		return false, err
	}
	hostops.ReportReady(out, r)
	return r.OK(), nil
}

// Typed logs, on the session's host, the hash of text about to be typed
// into the session of the run, in the run's typed-text log (the typed op).
// Text whose hash is not logged must not be typed: the run's prompt hook
// would take it for the operator's. A host whose engine predates the op
// has no prompt hook either, so its refusal of the op is no error.
func Typed(client Client, runID, text string) error {
	hash := session.TextHash(text)
	switch c := client.(type) {
	case Local:
		_, err := hostops.LogTyped(c.Deps, runID, hash)
		return err
	case SSH:
		var reply hostops.Typed
		err := c.call("typed", []string{"--run-id", runID, "--sha256", hash}, "", &reply)
		var refused *hostops.RefusedError
		if errors.As(err, &refused) && refused.Reason == `unknown op "typed"` {
			return nil
		}
		return err
	default:
		if c, ok := client.(interface {
			Typed(runID, hash string) error
		}); ok {
			return c.Typed(runID, hash)
		}
		return fmt.Errorf("host client does not log typed text")
	}
}
