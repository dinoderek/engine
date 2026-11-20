package session

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// TmuxLockDir is engine's data directory under the home directory, where the
// per-host window-creation lock lives.
const TmuxLockDir = ".local/share/engine"

// respawnRetryWait is how long Open waits before it tries respawn-pane once
// more: long enough for a server that is still settling after new-window.
const respawnRetryWait = time.Second

// ExecIn is Exec with dir as the working directory of every command. tmux
// starts its server in the directory of the process that first runs it, so a
// tmux driven through ExecIn(home) never has a server inside a worktree that
// is later removed. An empty dir fails every command rather than inherit the
// caller's directory.
func ExecIn(dir string) Runner {
	return func(name string, args ...string) (string, error) {
		if dir == "" {
			return "", errors.New("no home directory to run " + name + " in")
		}
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		return runCmd(cmd, name, args)
	}
}

// lockPath is the file that serialises window creation on this tmux server:
// one per server, in engine's data directory. Empty when t has no Home.
func (t *Tmux) lockPath() string {
	if t.Home == "" {
		return ""
	}
	server := "default"
	if t.Socket != "" {
		server = strings.NewReplacer("/", "_", `\`, "_").Replace(t.Socket)
	}
	return filepath.Join(t.Home, TmuxLockDir, "tmux-"+server+".lock")
}

// withLock runs fn while holding the host's tmux lock. Two engine processes
// that open windows at once would otherwise race each other's new-session and
// respawn-pane on one server.
func (t *Tmux) withLock(fn func() error) error {
	path := t.lockPath()
	if path == "" {
		return fn()
	}
	release, err := lock(path)
	if err != nil {
		return err
	}
	defer release()
	return fn()
}

func (t *Tmux) pause(d time.Duration) {
	sleep := t.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	sleep(d)
}

// respawn replaces the holding command with the real one. A failure is
// retried once after a short wait; a second failure says what tmux's server
// looked like, since the cause is usually the server's own state (a working
// directory that is gone) and not the command.
func (t *Tmux) respawn(info WindowInfo, w Window) error {
	args := []string{"respawn-pane", "-k", "-t", info.PaneID, "-c", formatEscape(w.Dir), w.Command}
	_, first := t.tmux(args...)
	if first == nil {
		return nil
	}
	t.pause(respawnRetryWait)
	_, second := t.tmux(args...)
	if second == nil {
		return nil
	}
	return fmt.Errorf("respawn-pane failed, and again after %s: first: %w; then: %w\ntmux server state (list-panes -a):\n%s", respawnRetryWait, first, second, t.serverState())
}

// serverState lists every pane with its server pid, pane pid, tty and cwd.
func (t *Tmux) serverState() string {
	out, err := t.tmux("list-panes", "-a", "-F", "server=#{pid} #{session_name}:#{window_id} #{pane_id} pid=#{pane_pid} tty=#{pane_tty} cwd=#{pane_current_path}")
	if err != nil {
		return "list-panes -a failed: " + err.Error()
	}
	return strings.TrimSpace(out)
}

// noServer reads tmux's complaint when no server answers: none is running or
// its socket does not exist. Any other failure, such as a permission error on
// the socket, is not "no server".
func noServer(stderr string) bool {
	return strings.Contains(stderr, "no server running") || strings.Contains(stderr, "No such file or directory")
}

// ServerPid is the pid of the tmux server, and false when no server runs.
func (t *Tmux) ServerPid() (int, bool, error) {
	out, err := t.tmux("display-message", "-p", "#{pid}")
	var exit *ExitError
	if errors.As(err, &exit) && noServer(exit.Stderr) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	var pid int
	if _, err := fmt.Sscanf(strings.TrimSpace(out), "%d", &pid); err != nil || pid <= 0 {
		return 0, false, fmt.Errorf("tmux printed %q for the server pid", strings.TrimSpace(out))
	}
	return pid, true, nil
}
