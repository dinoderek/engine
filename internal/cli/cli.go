// Package cli is the engine command line: it parses arguments and dispatches
// to the command that handles them.
package cli

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/dinoderek/engine/internal/controlcmd"
	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/remote"
	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/ui"
	"github.com/dinoderek/engine/internal/version"
)

const usage = `usage: engine <command>

commands:
  serve                     run the private control server (engine serve --help)
  repo, plan, task          manage server repositories, plans and tasks
  updates, inbox            read durable task updates or operator messages
  ask, reply, ack, resolve  exchange and resolve operator questions
  merge-queue, attempt      a repository's merge queue and execution attempts
  ui [--listen <addr>]      serve the read-only dashboard on loopback (127.0.0.1:7332)
  version                   print the engine version
  hook <entry> --harness <name> [args]
                            run a harness hook on the event on stdin: pre-tool-use
                            (the merge guard), permission (log, and deny or leave
                            to the harness's review in unattended sessions), state
                            (record a session's state), or the harness's own
  ps [--json]               list server tasks
  attach <task>             attach to a task's tmux window, here or over SSH
  tell <task> <text>        message a task; a lone - reads the text from stdin
  run <plan>                run a plan on the server: p<n>
  lint <plan>               check a plan: task sections, cycles, the acceptance
                            task and shared open questions
  start --check [--harness <name>] [--host <name>]
                            launch a throwaway session to find prompts that would
                            stop an unattended one
  setup [--host <name>]     bring a host to this engine's commit, plugin and config,
                            or check this one; exits 1 while any item is open
  host <op> --json          run one of this host's ops for a caller on any host:
                            sync-repo, spawn, status, collect or release
`

// Env is what a command reads and runs: the standard streams, the runners for
// outside CLIs, the environment, the home directory and the clock.
type Env struct {
	In     io.Reader
	Out    io.Writer
	Err    io.Writer
	Stream Streamer
	// Local runs the host's own CLIs (tmux, the harnesses, git) and returns their
	// standard output.
	Local session.Runner
	// Run runs git and gh for engine's clones.
	Run  session.RunFunc
	Tmux *session.Tmux
	// SSH runs ssh to reach another host's ops, and systemctl for a server
	// host that records a service manager; nil starts real processes.
	SSH remote.Exec
	// Sleep waits for a started server to settle; nil is time.Sleep.
	Sleep func(time.Duration)
	// Build is this engine's own build, whose commit engine setup installs on
	// another host.
	Build  version.Info
	Getenv func(string) string
	Home   string
	Now    func() time.Time
	// Detach starts this engine with args in the background, without waiting.
	Detach func(args ...string) error
	// Harnesses are the harness adapters; nil builds the registry's from
	// Local, Home, Getenv and Now.
	Harnesses harness.Adapters
}

// System is the Env of the running process.
func System() Env {
	home, _ := os.UserHomeDir()
	return Env{
		In: os.Stdin, Out: os.Stdout, Err: os.Stderr, Stream: StreamExec,
		Local: session.Exec, Run: session.RunCommand, Tmux: session.New(), Getenv: os.Getenv, Home: home, Now: time.Now, Build: version.Current(),
		Detach: detachSelf,
	}
}

// Run executes the command named by args and returns the process exit code.
func Run(args []string, env Env) int {
	if len(args) == 0 {
		fmt.Fprint(env.Err, usage)
		return 2
	}
	if serverCommand(args) {
		return controlcmd.Main(args, controlEnv(env))
	}
	switch args[0] {
	case "version":
		fmt.Fprintln(env.Out, version.Current())
		return 0
	case "hook":
		return runHook(args[1:], env)
	case "start":
		return runStart(args[1:], env)
	case "ps", "attach", "tell", "run", "lint":
		fmt.Fprint(env.Err, removedUsage[args[0]])
		return 2
	case "host":
		return runHost(args[1:], env)
	case "setup":
		return runSetup(args[1:], env)
	case "ui":
		return ui.Main(args[1:], ui.Env{Out: env.Out, Err: env.Err, Now: env.Now, Connect: func() (ui.Caller, error) {
			return controlcmd.Connect(controlEnv(env))
		}})
	case "help", "-h", "--help":
		fmt.Fprint(env.Out, usage)
		return 0
	default:
		fmt.Fprintf(env.Err, "engine: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

// runHost runs one of this host's ops for a caller, which may be on another
// host.
func runHost(args []string, env Env) int {
	return hostops.Main(args, hostDeps(env), env.In, env.Out, env.Err)
}

// hostDeps is what this host's ops run with.
func hostDeps(env Env) hostops.Deps {
	return hostops.Deps{Run: env.Run, Probe: session.RunCommandWithin(session.ProbeLimit), Teardown: session.RunCommandWithin(session.TeardownLimit), Exec: env.Local, Tmux: env.Tmux, Home: env.Home, Getenv: env.Getenv, Now: env.Now, JoinRemote: true,
		Harnesses: adapters(env)}
}
