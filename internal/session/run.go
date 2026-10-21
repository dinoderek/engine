package session

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// RunFunc runs name with args in dir ("" for the current directory), adding
// env to the process environment, and returns its combined output. Tests
// replace it, so the outside CLIs (git, gh) are never started for real.
type RunFunc func(dir string, env []string, name string, args ...string) (string, error)

// RunCommand is the RunFunc that starts real processes.
func RunCommand(dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		err = fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), err
}
