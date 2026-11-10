package remote

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/dinoderek/engine/internal/hostops"
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
