package tracker

import (
	"errors"
	"io"
	"os"
	"os/exec"
)

// Streams are the standard streams a process is connected to.
type Streams struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

// Streamer runs name with args, adding env to the process environment and
// connecting it to streams. It returns the process's exit code; the error is
// only for a process that could not run at all.
type Streamer func(env []string, streams Streams, name string, args ...string) (int, error)

// StreamExec is the Streamer that starts real processes. A later entry in
// env replaces the same key inherited from this process.
func StreamExec(env []string, streams Streams, name string, args ...string) (int, error) {
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = streams.In, streams.Out, streams.Err
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return 1, err
	}
	return 0, nil
}
