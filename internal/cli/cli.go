// Package cli is the engine command line: it parses arguments and dispatches
// to the command that handles them.
package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dinoderek/engine/internal/controlcmd"
	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/harness/registry"
	"github.com/dinoderek/engine/internal/hook"
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
  hook pre-tool-use         the merge guard, for Claude Code and Codex: reads the event on stdin
  hook permission           log permission requests in unattended sessions, and deny
                            Claude Code's (Codex's auto-review decides its own)
  hook statusline           record Claude quota and print its status line
  hook limit                record or clear a Claude usage limit
  hook state [--harness h]  record a session's state from its hook event
  hook codex-label <run-dir> <thread>  title a joined Codex thread and file it under its repo's project
  ps [--json]               list server tasks
  attach <task>             attach to a task's tmux window, here or over SSH
  tell <task> <text>        message a task; a lone - reads the text from stdin
  run <plan|task>           run a plan, or one task, on the server: p<n> or t<n>
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
	// Local runs the host's own CLIs (tmux, claude, git) and returns their
	// standard output.
	Local session.Runner
	// Run runs git and gh for engine's clones.
	Run  session.RunFunc
	Tmux *session.Tmux
	// SSH runs ssh to reach another host's ops; nil starts real processes.
	SSH remote.Exec
	// Build is this engine's own build, whose commit engine setup installs on
	// another host.
	Build  version.Info
	Getenv func(string) string
	Home   string
	Now    func() time.Time
	// Detach starts this engine with args in the background, without waiting.
	Detach func(args ...string) error
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

// runHook runs a harness hook. An unknown hook fails open (exit 1, which
// blocks nothing) rather than 2, which a harness reads as "block": a plugin
// can be newer than the engine binary it calls.
func runHook(args []string, env Env) int {
	if len(args) == 1 && args[0] == "pre-tool-use" {
		return preToolUse(env)
	}
	if len(args) == 1 && args[0] == "permission" {
		return permissionHook(env)
	}
	if len(args) == 1 && args[0] == "statusline" {
		if err := hook.Statusline(env.In, env.Out, env.Getenv(session.RunDirVar), env.Now()); err != nil {
			fmt.Fprintln(env.Err, "engine hook statusline:", err)
			return 1
		}
		return 0
	}
	if len(args) == 1 && args[0] == "limit" {
		if err := hook.Limit(env.In, env.Getenv(session.RunDirVar), env.Now()); err != nil {
			fmt.Fprintln(env.Err, "engine hook limit:", err)
			return 1
		}
		return 0
	}
	if len(args) == 1 && args[0] == "state" {
		return stateHook(env, "")
	}
	if len(args) == 3 && args[0] == "state" && args[1] == "--harness" {
		return stateHook(env, args[2])
	}
	if len(args) == 3 && args[0] == "codex-label" {
		return codexLabelHook(env, args[1], args[2])
	}
	fmt.Fprintf(env.Err, "engine hook: unknown hook %q; update engine (go install ./cmd/engine)\n", strings.Join(args, " "))
	return 1
}

// runHost runs one of this host's ops for a caller, which may be on another
// host.
func runHost(args []string, env Env) int {
	return hostops.Main(args, hostDeps(env), env.In, env.Out, env.Err)
}

// hostDeps is what this host's ops run with.
func hostDeps(env Env) hostops.Deps {
	return hostops.Deps{Run: env.Run, Probe: session.RunCommandWithin(session.ProbeLimit), Exec: env.Local, Tmux: env.Tmux, Home: env.Home, Getenv: env.Getenv, Now: env.Now, JoinRemote: true,
		Harnesses: registry.New(harness.Env{Exec: env.Local, Home: env.Home, Getenv: env.Getenv, Now: env.Now})}
}

// permissionHook runs the permission hook, logging to the run directory of
// the session's run, when there is one.
func permissionHook(env Env) int {
	run, in := findHookRun(env)
	h := hook.Permission{Out: env.Out, Err: env.Err, Unattended: run.unattended, Codex: run.harness == codexHarness, Now: env.Now()}
	if run.dir != "" {
		f, err := os.OpenFile(filepath.Join(run.dir, session.PermissionLog), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			fmt.Fprintln(env.Err, "engine hook: open the permission log:", err)
		} else {
			h.Log = f
			code := h.Handle(in)
			if err := f.Close(); err != nil {
				fmt.Fprintln(env.Err, "engine hook: close the permission log:", err)
			}
			return code
		}
	}
	return h.Handle(in)
}

// stateHook records the session's state in the run directory of the session's
// run. Outside an engine run there is none, and it does nothing. With harness
// set, it records only a run of that harness: a Claude reviewer that a Codex
// executor starts inherits the Codex run's variables, and its turns are not
// that run's.
func stateHook(env Env, harness string) int {
	run, in := findHookRun(env)
	if run.dir == "" || harness != "" && run.harness != "" && harness != run.harness {
		return 0
	}
	h := hook.State{Err: env.Err, Unattended: run.unattended}
	h.Write = func(sig hook.Signal) error {
		if err := session.WriteHarnessState(run.dir, session.HarnessState{State: sig.State, Event: sig.Event, At: env.Now(), SessionID: sig.SessionID, TurnID: sig.TurnID}); err != nil {
			return err
		}
		labelCodexThread(env, run, sig)
		return nil
	}
	return h.Handle(in)
}
