// Command dev implements the ./dev verbs. Run it through ./dev, which pins the
// toolchain from go.mod.
package main

import (
	"os"

	"github.com/dinoderek/engine/devtools/internal/dev"
)

func main() {
	os.Exit(dev.Main(os.Args[1:]))
}
