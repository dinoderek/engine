// Package checks implements the lanes that are not a single external command:
// `./dev check <name>` runs one. Each check prints its findings and returns
// the lane's exit code.
package checks

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/dinoderek/engine/devtools/internal/baseline"
	"github.com/dinoderek/engine/devtools/internal/repo"
)

// Env is what a check needs from its surroundings.
type Env struct {
	Root      string      // repository root
	Run       repo.Runner // runs git and go
	Out       io.Writer
	GoVersion string // toolchain running the check, e.g. "go1.27.2"
}

// Names lists the checks for usage messages.
const Names = "format, lint, coverage, file-length, imports, versions"

// Run dispatches to the check called name.
func Run(e Env, name string, args []string) int {
	switch name {
	case "format":
		return Format(e)
	case "lint":
		return Lint(e)
	case "coverage":
		return Coverage(e, args)
	case "file-length":
		return FileLength(e, args)
	case "imports":
		return Imports(e)
	case "versions":
		return Versions(e)
	default:
		fmt.Fprintf(e.Out, "dev: unknown check %q (have %s)\n", name, Names)
		return 2
	}
}

// report prints problems, or ok when there are none, and returns the exit code.
func (e Env) report(lane string, problems []string, ok string) int {
	for _, p := range problems {
		fmt.Fprintf(e.Out, "%s: %s\n", lane, p)
	}
	if len(problems) > 0 {
		fmt.Fprintf(e.Out, "%s: %d problem(s)\n", lane, len(problems))
		return 1
	}
	fmt.Fprintf(e.Out, "%s: %s\n", lane, ok)
	return 0
}

// fail prints an error that stopped a check from running.
func (e Env) fail(lane string, err error) int {
	fmt.Fprintf(e.Out, "%s: %v\n", lane, err)
	return 1
}

// readBaseline parses a baseline file under the root.
func (e Env) readBaseline(rel string, keyFields int) (baseline.Counts, error) {
	text, err := os.ReadFile(filepath.Join(e.Root, rel))
	if err != nil {
		return nil, err
	}
	return baseline.Parse(rel, string(text), keyFields)
}

// goFiles lists the repository's Go files.
func (e Env) goFiles() ([]string, error) {
	files, err := repo.Files(e.Run, e.Root)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, f := range files {
		if strings.HasSuffix(f, ".go") {
			out = append(out, f)
		}
	}
	return out, nil
}

// flags parses a check's arguments, reporting errors to e.Out.
func (e Env) flags(name string, args []string, define func(*flag.FlagSet)) error {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(e.Out)
	define(fs)
	return fs.Parse(args)
}

// Format fails when a Go file is not gofmt-formatted. It runs the gofmt of the
// toolchain go.mod pins, not whichever gofmt is on PATH.
func Format(e Env) int {
	const lane = "format"
	goroot, err := e.Run(e.Root, "go", "env", "GOROOT")
	if err != nil {
		return e.fail(lane, err)
	}
	files, err := e.goFiles()
	if err != nil {
		return e.fail(lane, err)
	}
	if len(files) == 0 {
		return e.report(lane, nil, "no Go files")
	}
	gofmt := filepath.Join(strings.TrimSpace(goroot), "bin", "gofmt")
	out, err := e.Run(e.Root, gofmt, append([]string{"-l"}, files...)...)
	if err != nil {
		return e.fail(lane, fmt.Errorf("%w\n%s", err, out))
	}
	var problems []string
	for _, f := range strings.Fields(out) {
		problems = append(problems, f+": not gofmt-formatted; run gofmt -w "+f)
	}
	return e.report(lane, problems, fmt.Sprintf("%d Go files formatted", len(files)))
}

// removeAll deletes a check's scratch directory. Failing to is harmless: it is
// under the system temp directory.
func removeAll(dir string) { _ = os.RemoveAll(dir) }
