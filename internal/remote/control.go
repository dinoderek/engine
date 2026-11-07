package remote

import (
	"encoding/json"
	"fmt"

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
