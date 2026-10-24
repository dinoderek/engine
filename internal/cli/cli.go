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

	"github.com/dinoderek/engine/internal/attach"
	"github.com/dinoderek/engine/internal/hook"
	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/start"
	"github.com/dinoderek/engine/internal/tell"
	"github.com/dinoderek/engine/internal/tracker"
	"github.com/dinoderek/engine/internal/version"
)

const usage = `usage: engine <command>

commands:
  version                   print the engine version
  labels sync <owner/repo>  create or update the engine: labels in a repo
  hook pre-tool-use         the Claude Code merge guard: reads the event on stdin
  hook permission           deny and log permission requests in unattended sessions
  gh <args...>              run gh as engine's machine user
  ps [--json]               list the claimed tasks and the state of their sessions
  attach <repo>-<task>      attach to a task's tmux window, here or over SSH
  tell <repo>-<task> <text> comment on a task's issue and point its session at it
  start <owner/repo#n> [--tier deep|standard|light] [--mode hands-off|gated]
                            launch an executor session for a task on this machine
`

// Env is what a command reads and runs: the standard streams, the runners for
// outside CLIs, the environment, the home directory and the clock.
type Env struct {
	In     io.Reader
	Out    io.Writer
	Err    io.Writer
	Exec   tracker.Runner
	Stream tracker.Streamer
	// Local runs the host's own CLIs (tmux, claude, git) and returns their
	// standard output.
	Local session.Runner
	// Run runs git and gh for engine's clones.
	Run    session.RunFunc
	Tmux   *session.Tmux
	Getenv func(string) string
	Home   string
	Now    func() time.Time
}

// System is the Env of the running process.
func System() Env {
	home, _ := os.UserHomeDir() // "" makes tracker.Token refuse the token file
	return Env{
		In: os.Stdin, Out: os.Stdout, Err: os.Stderr, Exec: tracker.Exec, Stream: tracker.StreamExec,
		Local: session.Exec, Run: session.RunCommand, Tmux: session.New(), Getenv: os.Getenv, Home: home, Now: time.Now,
	}
}

// Run executes the command named by args and returns the process exit code.
func Run(args []string, env Env) int {
	if len(args) == 0 {
		fmt.Fprint(env.Err, usage)
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Fprintln(env.Out, version.Current())
		return 0
	case "labels":
		if len(args) != 3 || args[1] != "sync" {
			fmt.Fprint(env.Err, "usage: engine labels sync <owner/repo>\n")
			return 2
		}
		return syncLabels(args[2], env)
	case "hook":
		return runHook(args[1:], env)
	case "gh":
		return runGh(args[1:], env)
	case "ps":
		return runPs(args[1:], env)
	case "attach":
		return runAttach(args[1:], env)
	case "start":
		return runStart(args[1:], env)
	case "tell":
		return runTell(args[1:], env)
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
		return hook.PreToolUse(env.In, env.Err)
	}
	if len(args) == 1 && args[0] == "permission" {
		return permissionHook(env)
	}
	fmt.Fprintf(env.Err, "engine hook: unknown hook %q; update engine (go install ./cmd/engine)\n", strings.Join(args, " "))
	return 1
}

// runGh runs gh as engine's machine user, with its streams and exit code, so
// a session's comments and @mentions reach the operator without the token
// appearing on a command line.
func runGh(args []string, env Env) int {
	token, err := tracker.Token(env.Getenv, env.Home)
	if err != nil {
		fmt.Fprintln(env.Err, "engine gh:", err)
		return 1
	}
	streams := tracker.Streams{In: env.In, Out: env.Out, Err: env.Err}
	code, err := env.Stream([]string{"GH_TOKEN=" + token}, streams, "gh", args...)
	if err != nil {
		fmt.Fprintln(env.Err, "engine gh:", err)
	}
	return code
}

// runAttach connects this terminal to a task's tmux window. On success it
// replaces this process, so it returns only on failure.
func runAttach(args []string, env Env) int {
	if len(args) != 1 {
		fmt.Fprint(env.Err, "usage: engine attach <repo>-<task>\n")
		return 2
	}
	if err := attach.Attach(args[0], attach.System(env.Exec, env.Getenv, env.Home)); err != nil {
		fmt.Fprintln(env.Err, "engine attach:", err)
		return 1
	}
	return 0
}

// runTell comments on a task and types a pointer into its window. A failure
// after the comment landed still prints the comment's address.
func runTell(args []string, env Env) int {
	if len(args) < 2 {
		fmt.Fprint(env.Err, "usage: engine tell <repo>-<task> <text...>\n")
		return 2
	}
	told, err := tell.Tell(args[0], strings.Join(args[1:], " "), tell.Deps{Gh: env.Exec, Tmux: env.Tmux, Getenv: env.Getenv, Home: env.Home})
	if err != nil {
		fmt.Fprintln(env.Err, "engine tell:", err)
		return 1
	}
	fmt.Fprintf(env.Out, "commented on %s: %s\ntyped a pointer into %s\n", told.Task, told.URL, told.Window)
	return 0
}

func syncLabels(repo string, env Env) int {
	token, err := tracker.Token(env.Getenv, env.Home)
	if err == nil {
		err = tracker.SyncLabels(env.Exec, token, repo, env.Out)
	}
	if err != nil {
		fmt.Fprintln(env.Err, "engine labels sync:", err)
		return 1
	}
	return 0
}

const startUsage = "usage: engine start <owner/repo#n> [--tier deep|standard|light] [--mode hands-off|gated]\n"

// runStart parses engine start's arguments, in any order, and starts the
// task's session.
func runStart(args []string, env Env) int {
	opts, err := parseStart(args)
	if err != nil {
		fmt.Fprintf(env.Err, "engine start: %v\n%s", err, startUsage)
		return 2
	}
	deps := start.Deps{GH: env.Exec, Run: env.Run, Tmux: env.Tmux, Home: env.Home, Getenv: env.Getenv, Now: env.Now}
	if err := start.Run(opts, deps, env.Out); err != nil {
		fmt.Fprintln(env.Err, "engine start:", err)
		return 1
	}
	return 0
}

// parseStart reads the task and the --tier and --mode flags, as `--flag
// value` or `--flag=value`, in any order.
func parseStart(args []string) (start.Options, error) {
	var opts start.Options
	var ref string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, value, inline := strings.Cut(arg, "=")
		dst := map[string]*string{"--tier": &opts.Tier, "--mode": &opts.Mode}[name]
		switch {
		case dst != nil && inline && value != "":
			*dst = value
		case dst != nil && !inline && i+1 < len(args) && args[i+1] != "":
			i++
			*dst = args[i]
		case dst != nil:
			return opts, fmt.Errorf("%s needs a value", arg)
		case strings.HasPrefix(arg, "-"):
			return opts, fmt.Errorf("unknown flag %s", arg)
		case ref != "":
			return opts, fmt.Errorf("one task at a time, got %s and %s", ref, arg)
		default:
			ref = arg
		}
	}
	if ref == "" {
		return opts, fmt.Errorf("no task")
	}
	r, err := tracker.ParseRef(ref)
	opts.Ref = r
	return opts, err
}

// permissionHook runs the permission hook, logging to the run directory that
// engine start exported, when there is one.
func permissionHook(env Env) int {
	h := hook.Permission{Out: env.Out, Err: env.Err, Unattended: env.Getenv(session.UnattendedVar) == "1", Now: env.Now()}
	if dir := env.Getenv(session.RunDirVar); dir != "" {
		f, err := os.OpenFile(filepath.Join(dir, session.PermissionLog), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			fmt.Fprintln(env.Err, "engine hook: open the permission log:", err)
		} else {
			h.Log = f
			code := h.Handle(env.In)
			if err := f.Close(); err != nil {
				fmt.Fprintln(env.Err, "engine hook: close the permission log:", err)
			}
			return code
		}
	}
	return h.Handle(env.In)
}
