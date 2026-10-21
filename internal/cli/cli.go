// Package cli is the engine command line: it parses arguments and dispatches
// to the command that handles them.
package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

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
	home, _ := os.UserHomeDir() // "" makes tracker.Token refuse the token file
	return Env{In: os.Stdin, Out: os.Stdout, Err: os.Stderr, Exec: tracker.Exec, Getenv: os.Getenv, Home: home}
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
	fmt.Fprintf(env.Err, "engine hook: unknown hook %q; update engine (go install ./cmd/engine)\n", strings.Join(args, " "))
	return 1
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
