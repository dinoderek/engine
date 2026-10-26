package cli

import (
	"fmt"
	"strings"

	"github.com/dinoderek/engine/internal/start"
	"github.com/dinoderek/engine/internal/tracker"
)

const startUsage = "usage: engine start <owner/repo#n> [--harness <name>] [--tier deep|standard|light] [--mode hands-off|gated] [--host <name>]\n       engine start --check [--harness <name>] [--host <name>]\n"

// startArgs are engine start's arguments: a task to start, or a check.
type startArgs struct {
	start.Options
	Check bool
}

// runStart parses engine start's arguments, in any order, and starts the
// task's session, or runs the preflight check.
func runStart(args []string, env Env) int {
	a, err := parseStart(args)
	if err != nil {
		fmt.Fprintf(env.Err, "engine start: %v\n%s", err, startUsage)
		return 2
	}
	if a.Check {
		return runCheck(a.Host, a.Harness, env)
	}
	if err := start.Run(a.Options, startDeps(env), env.Out); err != nil {
		fmt.Fprintln(env.Err, "engine start:", err)
		return 1
	}
	return 0
}

// runCheck runs the preflight check of harness (Claude when empty), on host,
// which is this one when empty, and exits 1 when an unattended session there
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

// startDeps is what engine start, and engine run's starts, launch with.
func startDeps(env Env) start.Deps {
	return start.Deps{GH: env.Exec, Run: env.Run, Exec: env.Local, Tmux: env.Tmux, SSH: env.SSH, Home: env.Home, Getenv: env.Getenv, Now: env.Now}
}

// parseStart reads the task and the --harness, --tier, --mode, --host and
// --check flags, as `--flag value` or `--flag=value`, in any order. A check
// takes only --harness and --host.
func parseStart(args []string) (startArgs, error) {
	var a startArgs
	var ref string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, value, inline := strings.Cut(arg, "=")
		dst := map[string]*string{"--harness": &a.Harness, "--tier": &a.Tier, "--mode": &a.Mode, "--host": &a.Host}[name]
		switch {
		case arg == "--check":
			a.Check = true
		case dst != nil && inline && value != "":
			*dst = value
		case dst != nil && !inline && i+1 < len(args) && args[i+1] != "":
			i++
			*dst = args[i]
		case dst != nil:
			return a, fmt.Errorf("%s needs a value", arg)
		case strings.HasPrefix(arg, "-"):
			return a, fmt.Errorf("unknown flag %s", arg)
		case ref != "":
			return a, fmt.Errorf("one task at a time, got %s and %s", ref, arg)
		default:
			ref = arg
		}
	}
	if a.Check {
		if ref != "" || a.Tier != "" || a.Mode != "" {
			return a, fmt.Errorf("--check takes no task, --tier or --mode")
		}
		return a, nil
	}
	if ref == "" {
		return a, fmt.Errorf("no task")
	}
	r, err := tracker.ParseRef(ref)
	a.Ref = r
	return a, err
}
