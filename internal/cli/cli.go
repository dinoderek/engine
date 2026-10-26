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
	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/lint"
	"github.com/dinoderek/engine/internal/remote"
	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/tell"
	"github.com/dinoderek/engine/internal/tracker"
	"github.com/dinoderek/engine/internal/version"
)

const usage = `usage: engine <command>

commands:
  version                   print the engine version
  labels sync <owner/repo>  create or update the engine: labels in a repo
  hook pre-tool-use         the merge guard, for Claude Code and Codex: reads the event on stdin
  hook permission           log permission requests in unattended sessions, and deny
                            Claude Code's (Codex's auto-review decides its own)
  hook state                record a Codex session's state from its hook event
  gh <args...>              run gh as engine's machine user
  ps [--json]               list the claimed tasks and the state of their sessions
  attach <repo>-<task>      attach to a task's tmux window, here or over SSH
  tell <repo>-<task> <text> comment on a task's issue and point its session at it;
                            a lone - reads the text from stdin
  run <owner/repo#n>        coordinate a feature, or one task, on this host until
                            it is done
  lint <owner/repo#n>       check a feature's plan: card sections, keys, cycles,
                            the acceptance task and shared open questions
  start <owner/repo#n> [--harness <name>] [--tier deep|standard|light]
        [--mode hands-off|gated] [--host <name>]
                            launch an executor session for a task, on this machine
                            or on another one from hosts.toml
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
	Exec   tracker.Runner
	Stream tracker.Streamer
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
}

// System is the Env of the running process.
func System() Env {
	home, _ := os.UserHomeDir() // "" makes tracker.Token refuse the token file
	return Env{
		In: os.Stdin, Out: os.Stdout, Err: os.Stderr, Exec: tracker.Exec, Stream: tracker.StreamExec,
		Local: session.Exec, Run: session.RunCommand, Tmux: session.New(), Getenv: os.Getenv, Home: home, Now: time.Now, Build: version.Current(),
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
	case "lint":
		return runLint(args[1:], env)
	case "run":
		return runRun(args[1:], env, time.Sleep)
	case "host":
		return runHost(args[1:], env)
	case "setup":
		return runSetup(args[1:], env)
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
	if len(args) == 1 && args[0] == "state" {
		return stateHook(env)
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
		fmt.Fprint(env.Err, "usage: engine tell <repo>-<task> <text...|->\n")
		return 2
	}
	text, err := tell.Text(args[1:], env.In)
	if err != nil {
		fmt.Fprintln(env.Err, "engine tell:", err)
		return 2
	}
	told, err := tell.Tell(args[0], text, tell.Deps{Gh: env.Exec, Tmux: env.Tmux, Run: env.Local, Getenv: env.Getenv, Home: env.Home})
	if err != nil {
		fmt.Fprintln(env.Err, "engine tell:", err)
		return 1
	}
	fmt.Fprintf(env.Out, "commented on %s: %s\ntyped a pointer into %s on %s\n", told.Task, told.URL, told.Window, told.Host)
	return 0
}

// runLint lints a feature's plan and exits 1 when it breaks a rule.
func runLint(args []string, env Env) int {
	if len(args) != 1 {
		fmt.Fprint(env.Err, "usage: engine lint <owner/repo#n>\n")
		return 2
	}
	ref, err := tracker.ParseRef(args[0])
	if err != nil {
		fmt.Fprintln(env.Err, "engine lint:", err)
		return 2
	}
	token, err := tracker.Token(env.Getenv, env.Home)
	if err != nil {
		fmt.Fprintln(env.Err, "engine lint:", err)
		return 1
	}
	f, err := tracker.ReadFeature(env.Exec, token, ref)
	if err != nil {
		fmt.Fprintln(env.Err, "engine lint:", err)
		return 1
	}
	problems := lint.Lint(f)
	for _, p := range problems {
		fmt.Fprintln(env.Out, p)
	}
	if len(problems) > 0 {
		fmt.Fprintf(env.Out, "%s: %d problem(s)\n", ref, len(problems))
		return 1
	}
	fmt.Fprintf(env.Out, "%s: the plan passes, %d tasks\n", ref, len(f.Tasks))
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

// runHost runs one of this host's ops for a caller, which may be on another
// host.
func runHost(args []string, env Env) int {
	return hostops.Main(args, hostDeps(env), env.In, env.Out, env.Err)
}

// hostDeps is what this host's ops run with.
func hostDeps(env Env) hostops.Deps {
	return hostops.Deps{Run: env.Run, Exec: env.Local, Tmux: env.Tmux, Home: env.Home, Getenv: env.Getenv, Now: env.Now}
}

// permissionHook runs the permission hook, logging to the run directory that
// engine start exported, when there is one.
func permissionHook(env Env) int {
	h := hook.Permission{Out: env.Out, Err: env.Err, Unattended: env.Getenv(session.UnattendedVar) == "1",
		Codex: env.Getenv(session.HarnessVar) == hostops.Codex, Now: env.Now()}
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

// stateHook records the session's state in the run directory that engine
// start exported. Outside an engine run there is none, and it does nothing.
func stateHook(env Env) int {
	dir := env.Getenv(session.RunDirVar)
	if dir == "" {
		return 0
	}
	h := hook.State{Err: env.Err, Unattended: env.Getenv(session.UnattendedVar) == "1"}
	h.Write = func(sig hook.Signal) error {
		return session.WriteHarnessState(dir, session.HarnessState{State: sig.State, Event: sig.Event, At: env.Now(), SessionID: sig.SessionID})
	}
	return h.Handle(env.In)
}
