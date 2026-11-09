package cli

import (
	"fmt"
	"strings"

	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/start"
)

const startUsage = "usage: engine start --check [--harness <name>] [--host <name>]\n       (to run a task: engine run <task-id>)\n"

// checkArgs are engine start --check's arguments.
type checkArgs struct{ Harness, Host string }

// runStart runs the preflight check, the only thing engine start does: a
// task runs through the server, with engine run.
func runStart(args []string, env Env) int {
	a, err := parseCheck(args)
	if err != nil {
		fmt.Fprintf(env.Err, "engine start: %v\n%s", err, startUsage)
		return 2
	}
	return runCheck(a.Host, a.Harness, env)
}

// runCheck runs the preflight check of harness (the default when empty), on
// host (this one when empty), and exits 1 when an unattended session there
// would stop.
func runCheck(host, harness string, env Env) int {
	ok, err := start.Check(startDeps(env), host, harness, env.Out)
	if err != nil {
		fmt.Fprintln(env.Err, "engine start --check:", err)
		return 1
	}
	return checkCode(ok)
}

// checkCode is the exit code of a check: 1 when a session would stop.
func checkCode(ok bool) int {
	if ok {
		return 0
	}
	return 1
}

// startDeps is what the preflight check launches with.
func startDeps(env Env) start.Deps {
	return start.Deps{Run: env.Run, Probe: session.RunCommandWithin(session.ProbeLimit), Exec: env.Local, Tmux: env.Tmux, SSH: env.SSH, Home: env.Home, Getenv: env.Getenv, Now: env.Now}
}

// parseCheck reads --check and the --harness and --host flags, as `--flag
// value` or `--flag=value`, in any order. --check is required.
func parseCheck(args []string) (checkArgs, error) {
	var a checkArgs
	var check bool
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, value, inline := strings.Cut(arg, "=")
		dst := map[string]*string{"--harness": &a.Harness, "--host": &a.Host}[name]
		switch {
		case arg == "--check":
			check = true
		case dst != nil && inline && value != "":
			*dst = value
		case dst != nil && !inline && i+1 < len(args) && args[i+1] != "":
			i++
			*dst = args[i]
		case dst != nil:
			return a, fmt.Errorf("%s needs a value", arg)
		case strings.HasPrefix(arg, "-"):
			return a, fmt.Errorf("unknown flag %s", arg)
		default:
			return a, fmt.Errorf("takes no task, got %s", arg)
		}
	}
	if !check {
		return a, fmt.Errorf("only --check is left")
	}
	return a, nil
}
