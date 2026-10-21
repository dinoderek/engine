// Command engine is the coordinator CLI.
package main

import (
	"os"

	"github.com/dinoderek/engine/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], cli.System()))
}
