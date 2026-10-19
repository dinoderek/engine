// Package cli is the engine command line: it parses arguments and dispatches
// to the command that handles them.
package cli

import (
	"fmt"
	"io"

	"github.com/dinoderek/engine/internal/version"
)

const usage = `usage: engine <command>

commands:
  version   print the engine version
`

// Run executes the command named by args and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Fprintln(stdout, version.Current())
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "engine: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}
