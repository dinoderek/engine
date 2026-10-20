// Package cli is the engine command line: it parses arguments and dispatches
// to the command that handles them.
package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/dinoderek/engine/internal/hook"
	"github.com/dinoderek/engine/internal/tracker"
	"github.com/dinoderek/engine/internal/version"
)

const usage = `usage: engine <command>

commands:
  version                   print the engine version
  labels sync <owner/repo>  create or update the engine: labels in a repo
  hook pre-tool-use         the Claude Code merge guard: reads the event on stdin
`

// Env is what a command reads and runs: the standard streams, the runner for
// outside CLIs, the environment and the home directory.
type Env struct {
	In     io.Reader
	Out    io.Writer
	Err    io.Writer
	Exec   tracker.Runner
	Getenv func(string) string
	Home   string
}

// System is the Env of the running process.
func System() Env {
	home, _ := os.UserHomeDir() // "" leaves the token file unfound
	return Env{In: os.Stdin, Out: os.Stdout, Err: os.Stderr, Exec: tracker.Exec, Getenv: os.Getenv, Home: home}
}

// Run executes the command named by args and returns the process exit code.
func Run(args []string, env Env) int {
	if len(args) == 0 {
		fmt.Fprint(env.Err, usage)
		return 2
	}
	switch {
	case args[0] == "version" && len(args) == 1:
		fmt.Fprintln(env.Out, version.Current())
		return 0
	case args[0] == "labels" && len(args) == 3 && args[1] == "sync":
		return syncLabels(args[2], env)
	case args[0] == "hook" && len(args) == 2 && args[1] == "pre-tool-use":
		return hook.PreToolUse(env.In, env.Err)
	case args[0] == "help" || args[0] == "-h" || args[0] == "--help":
		fmt.Fprint(env.Out, usage)
		return 0
	default:
		fmt.Fprintf(env.Err, "engine: unknown command %q\n\n%s", args, usage)
		return 2
	}
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
