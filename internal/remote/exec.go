package remote

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strings"

	"github.com/dinoderek/engine/internal/session"
)

// Exec runs a command with the given streams and returns its exit code. The
// error is set only when the command could not run at all, and the code is
// then -1.
type Exec func(in io.Reader, out, errOut io.Writer, name string, args ...string) (int, error)

// ExecCommand is the Exec that starts real processes.
func ExecCommand(in io.Reader, out, errOut io.Writer, name string, args ...string) (int, error) {
	cmd := exec.Command(name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = in, out, errOut
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &exit):
		return exit.ExitCode(), nil
	}
	return -1, fmt.Errorf("%s: %w", name, err)
}

// sshCode is the exit code ssh gives when it could not connect or log in.
const sshCode = 255

// tmuxNoSession is the exit code of a tmux command that finds no server or
// session, such as has-session.
const tmuxNoSession = 1

// TmuxRunner runs a tmux command on the host at address, over run, with the
// ssh options of every call (BatchMode, ConnectTimeout), so an unreachable
// host fails rather than prompts or hangs. The command goes to the remote
// shell as one quoted string. tmux exits 1 when
// the session or the server is missing, which Tmux.HasSession reads as "no
// session". Any other failure, such as ssh unable to connect (255) or a
// missing tmux (127), is reported as itself, never as a missing session.
func TmuxRunner(run session.Runner, address string) session.Runner {
	return func(name string, args ...string) (string, error) {
		out, err := run("ssh", append(slices.Clone(sshOptions), address, ShellJoin(append([]string{name}, args...)))...)
		var exit *session.ExitError
		if errors.As(err, &exit) && exit.Code != tmuxNoSession {
			detail := strings.TrimSpace(exit.Stderr)
			if detail == "" {
				detail = fmt.Sprintf("exit status %d", exit.Code)
			}
			return out, fmt.Errorf("ssh %s: %s", address, detail)
		}
		return out, err
	}
}

// ShellJoin quotes each word for a POSIX shell, as one command line.
func ShellJoin(words []string) string {
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
	}
	return strings.Join(quoted, " ")
}
