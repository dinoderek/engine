// Package attach connects the terminal to a task's tmux window. It finds the
// task's claim comment, then replaces this process with tmux on the claim's
// host: locally, or over SSH for a remote host. tmux is reached only through
// its CLI, behind a Runner, so tests replace it.
package attach

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/tracker"
)

// tmuxNoSession is the exit status of a tmux command that finds no server or
// session, such as has-session.
const tmuxNoSession = 1

// Deps are the programs and the environment attach reaches. System gives the
// ones a running engine uses; tests replace them.
type Deps struct {
	// Gh runs gh as the machine user, for the claim comments.
	Gh tracker.Runner
	// Run runs tmux and ssh and returns their standard output.
	Run session.Runner
	// Replace runs the program at path with argv, in place of this process
	// and with its environment. It returns only on failure.
	Replace  func(path string, argv []string) error
	LookPath func(file string) (string, error)
	Getenv   func(string) string
	Home     string
}

// System returns the Deps of the running process, with gh as the runner for
// the claim comments.
func System(gh tracker.Runner, getenv func(string) string, home string) Deps {
	return Deps{
		Gh:  gh,
		Run: session.Exec,
		Replace: func(path string, argv []string) error {
			return syscall.Exec(path, argv, os.Environ())
		},
		LookPath: exec.LookPath,
		Getenv:   getenv,
		Home:     home,
	}
}

// Attach connects to the tmux window of the task whose claim is named name,
// <repo>-<task>. On success it replaces the process, so it returns only an
// error.
func Attach(name string, d Deps) error {
	token, err := tracker.Token(d.Getenv, d.Home)
	if err != nil {
		return err
	}
	tasks, err := tracker.Claimed(d.Gh, token)
	if err != nil {
		return err
	}
	task, ok := tracker.FindClaim(tasks, name)
	if !ok {
		return fmt.Errorf("no claimed task named %s", name)
	}
	claim := task.Claim
	if claim.Host == "" {
		return fmt.Errorf("the claim of %s names no host", name)
	}
	cfg, local, err := hosts.Open(hosts.ConfigDir(d.Home))
	if err != nil {
		return err
	}
	if claim.Host == local.Name {
		return d.attachLocal(name, local.Name)
	}
	host, ok := cfg.Hosts[claim.Host]
	if !ok {
		return fmt.Errorf("host %q of %s is not in hosts.toml", claim.Host, name)
	}
	return d.attachRemote(name, host)
}

// attachLocal replaces the process with tmux on this host. Inside tmux it
// switches the client to the window instead, since a nested attach is refused.
func (d Deps) attachLocal(name, host string) error {
	tm := &session.Tmux{Run: d.Run, Bin: "tmux"}
	id, err := windowID(tm, name, host)
	if err != nil {
		return err
	}
	verb := "attach"
	if d.Getenv("TMUX") != "" {
		verb = "switch-client"
	}
	return d.replace("tmux", []string{"tmux", verb, "-t", target(id)})
}

// attachRemote replaces the process with ssh, which attaches on the host. The
// window is found first, over a plain ssh command, so the attach is by id too.
func (d Deps) attachRemote(name string, host hosts.Host) error {
	tm := &session.Tmux{Run: d.sshRunner(host.Address), Bin: "tmux"}
	id, err := windowID(tm, name, host.Name)
	if err != nil {
		return err
	}
	// ssh joins its trailing arguments and hands them to the remote shell, so
	// the remote command goes as one quoted string, the target included.
	remoteCmd := shellJoin([]string{"tmux", "attach", "-t", target(id)})
	return d.replace("ssh", []string{"ssh", "-t", host.Address, remoteCmd})
}

// windowID returns the id of the window named name in engine's tmux session.
// A window is targeted by id because tmux reads a dot in a name as a pane.
func windowID(tm *session.Tmux, name, host string) (string, error) {
	windows, err := tm.Windows()
	if err != nil {
		return "", err
	}
	for _, w := range windows {
		if w.Name == name {
			return w.ID, nil
		}
	}
	return "", fmt.Errorf("no tmux window named %s in the session %s on %s", name, session.Name, host)
}

// target is the tmux target of a window by id, in the session engine.
func target(id string) string { return "=" + session.Name + ":" + id }

// replace runs bin, found on PATH, with argv in place of this process.
func (d Deps) replace(bin string, argv []string) error {
	path, err := d.LookPath(bin)
	if err != nil {
		return fmt.Errorf("%s: %w", bin, err)
	}
	return d.Replace(path, argv)
}

// sshRunner runs a tmux command on the host at address. The command goes to
// the remote shell as one quoted string. tmux exits 1 when the session or the
// server is missing, which is what HasSession reads as "no session". Any
// other failure, such as ssh unable to connect (255) or a missing tmux (127),
// is reported as itself, never as a missing session.
func (d Deps) sshRunner(address string) session.Runner {
	return func(name string, args ...string) (string, error) {
		out, err := d.Run("ssh", address, shellJoin(append([]string{name}, args...)))
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

// shellJoin quotes each word for a POSIX shell, as one command line.
func shellJoin(words []string) string {
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
	}
	return strings.Join(quoted, " ")
}
