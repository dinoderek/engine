package session

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// ExecContext is the host CLI runner bounded by the server's lifetime.
func ExecContext(parent context.Context, limit time.Duration) Runner {
	return execContext(parent, limit, "")
}

// ExecContextIn is ExecContext with dir as the working directory of every
// command, as ExecIn is for Exec; an empty dir fails every command.
func ExecContextIn(parent context.Context, limit time.Duration, dir string) Runner {
	if dir == "" {
		return func(name string, _ ...string) (string, error) {
			return "", errors.New("no home directory to run " + name + " in")
		}
	}
	return execContext(parent, limit, dir)
}

func execContext(parent context.Context, limit time.Duration, dir string) Runner {
	return func(name string, args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(parent, limit)
		defer cancel()
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Dir = dir
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
		cmd.WaitDelay = time.Second
		var stderr strings.Builder
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if ctx.Err() != nil {
			return string(out), fmt.Errorf("%s: %w", name, ctx.Err())
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return string(out), &ExitError{Command: name + " " + strings.Join(args, " "), Code: exit.ExitCode(), Stderr: stderr.String()}
		}
		if err != nil {
			return string(out), fmt.Errorf("%s: %w", name, err)
		}
		return string(out), nil
	}
}
