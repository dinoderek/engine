package remote

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"syscall"
	"time"
)

// ExecContext bounds SSH calls by server lifetime. A canceled call has unknown outcome.
func ExecContext(parent context.Context, limit time.Duration) Exec {
	return func(in io.Reader, out, errOut io.Writer, name string, args ...string) (int, error) {
		ctx, cancel := context.WithTimeout(parent, limit)
		defer cancel()
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
		cmd.WaitDelay = time.Second
		cmd.Stdin, cmd.Stdout, cmd.Stderr = in, out, errOut
		err := cmd.Run()
		if ctx.Err() != nil {
			return -1, fmt.Errorf("%s: %w", name, ctx.Err())
		}
		if err == nil {
			return 0, nil
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode(), nil
		}
		return -1, fmt.Errorf("%s: %w", name, err)
	}
}
