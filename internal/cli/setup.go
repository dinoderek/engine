package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/dinoderek/engine/internal/remote"
	"github.com/dinoderek/engine/internal/setup"
	"github.com/dinoderek/engine/internal/start"
)

const setupUsage = "usage: engine setup [--host <name>]\n"

// parseSetup reads the host to set up, as `--host name` or `--host=name`; no
// flag is this host.
func parseSetup(args []string) (string, error) {
	switch {
	case len(args) == 0:
		return "", nil
	case len(args) == 2 && args[0] == "--host" && args[1] != "":
		return args[1], nil
	case len(args) == 1 && strings.HasPrefix(args[0], "--host=") && len(args[0]) > len("--host="):
		return strings.TrimPrefix(args[0], "--host="), nil
	}
	return "", fmt.Errorf("want --host <name> or nothing, got %q", strings.Join(args, " "))
}

// runSetup brings a host to this engine's commit, plugin and config, or checks
// this one. It exits 1 while any item is open.
func runSetup(args []string, env Env) int {
	host, err := parseSetup(args)
	if err != nil {
		fmt.Fprintf(env.Err, "engine setup: %v\n%s", err, setupUsage)
		return 2
	}
	exec := env.SSH
	if exec == nil {
		exec = remote.ExecCommand
	}
	d := setup.Deps{
		Home: env.Home, Getenv: env.Getenv, Exec: exec, GH: env.Exec, Info: env.Build,
		Check: func(out io.Writer) (bool, error) { return start.Check(startDeps(env), host, "", out) },
	}
	closed, err := setup.Run(host, d, env.Out)
	if err != nil {
		fmt.Fprintln(env.Err, "engine setup:", err)
		return 1
	}
	return checkCode(closed)
}
