package hostops

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// TmuxCwdGone is a tmux server whose working directory no longer exists. A
// server engine started never has one (every tmux command runs from the home
// directory); an older server may, and every window it creates then fails.
type TmuxCwdGone struct {
	Pid int    `json:"pid"`
	Dir string `json:"dir"`
	// Reason says why the host takes no new session, and the command that
	// restarts the server.
	Reason string `json:"reason"`
}

// deletedSuffix is what Linux appends to the target of /proc/<pid>/cwd when
// the directory has been removed.
const deletedSuffix = " (deleted)"

// tmuxCwdGone looks at the working directory of the tmux server pid. One
// whose directory exists is nil. A directory that cannot be read is an
// error: the check is then unknown, not passed.
func (d Deps) tmuxCwdGone(pid int) (*TmuxCwdGone, error) {
	dir, err := d.serverCwd(runtime.GOOS, pid)
	if err != nil {
		return nil, err
	}
	dir, deleted := strings.CutSuffix(dir, deletedSuffix)
	if !deleted {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return nil, nil
		}
	}
	return &TmuxCwdGone{Pid: pid, Dir: dir, Reason: fmt.Sprintf(
		"tmux server %d runs in %s, which no longer exists, so new windows fail; once its sessions are finished, restart it with `%s` (engine never kills a server that holds sessions)",
		pid, dir, d.killServer())}, nil
}

// killServer is the command that stops the tmux server engine uses.
func (d Deps) killServer() string {
	if d.Tmux.Socket != "" {
		return "tmux -L " + d.Tmux.Socket + " kill-server"
	}
	return "tmux kill-server"
}

// serverCwd reads a process's working directory: /proc on Linux, lsof on
// macOS.
func (d Deps) serverCwd(goos string, pid int) (string, error) {
	if goos == "darwin" {
		out, err := d.Exec("lsof", "-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-Fn")
		if err != nil {
			return "", fmt.Errorf("read tmux server %d's directory: %w", pid, err)
		}
		return parseLsofCwd(out)
	}
	out, err := d.Exec("readlink", fmt.Sprintf("/proc/%d/cwd", pid))
	if err != nil {
		return "", fmt.Errorf("read tmux server %d's directory: %w", pid, err)
	}
	dir := strings.TrimSpace(out)
	if dir == "" {
		return "", fmt.Errorf("read tmux server %d's directory: readlink printed nothing", pid)
	}
	return dir, nil
}

// parseLsofCwd takes the path from `lsof -Fn` output: one field per line,
// the name field starting with n.
func parseLsofCwd(out string) (string, error) {
	for _, line := range strings.Split(out, "\n") {
		if dir, ok := strings.CutPrefix(line, "n"); ok && dir != "" {
			return dir, nil
		}
	}
	return "", fmt.Errorf("lsof printed no working directory: %q", strings.TrimSpace(out))
}
