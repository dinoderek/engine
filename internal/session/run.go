package session

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
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

// RunCommandWithin is RunCommand under a deadline: past it, the process and
// everything it started (its process group) are killed, its pipes are closed a
// second later even if something still holds them, and the call returns what
// it printed with an error.
func RunCommandWithin(limit time.Duration) RunFunc {
	return func(dir string, env []string, name string, args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), limit)
		defer cancel()
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), env...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
		cmd.WaitDelay = time.Second
		out, err := cmd.CombinedOutput()
		if ctx.Err() != nil {
			err = fmt.Errorf("%s %s: no answer within %s", name, strings.Join(args, " "), limit)
		}
		return string(out), err
	}
}
