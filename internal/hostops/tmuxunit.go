package hostops

import (
	"fmt"
	"runtime"

	"github.com/dinoderek/engine/internal/session"
)

// TmuxUnit is a tmux server inside a systemd service's cgroup. Stopping or
// restarting the service kills the server and every window in it. A server
// engine starts from inside a service has a scope of its own; one started
// before that, or where systemd-run was missing or refused, may not.
type TmuxUnit struct {
	Pid  int    `json:"pid"`
	Unit string `json:"unit"`
	// Reason says what stopping the unit does, and the command that
	// restarts the server outside it.
	Reason string `json:"reason"`
}

// readServer fills in what status says about the tmux server itself: a
// working directory that is gone, and a service that would kill it. No
// server running is neither.
func (s *Status) readServer(d Deps) {
	pid, running, err := d.Tmux.ServerPid()
	if err != nil {
		s.TmuxCwdError, s.TmuxUnitError = err.Error(), err.Error()
		return
	}
	if !running {
		return
	}
	if s.TmuxCwdGone, err = d.tmuxCwdGone(pid); err != nil {
		s.TmuxCwdError = err.Error()
	}
	if s.TmuxUnit, err = d.tmuxUnit(runtime.GOOS, pid); err != nil {
		s.TmuxUnitError = err.Error()
	}
}

// tmuxUnit reads the cgroup of the tmux server pid, on Linux only. A server
// in a scope, or outside systemd, is nil. A cgroup that cannot be read is an
// error: the check is then unknown, not passed.
func (d Deps) tmuxUnit(goos string, pid int) (*TmuxUnit, error) {
	if goos != "linux" {
		return nil, nil
	}
	out, err := d.Exec("cat", fmt.Sprintf("/proc/%d/cgroup", pid))
	if err != nil {
		return nil, fmt.Errorf("read tmux server %d's cgroup: %w", pid, err)
	}
	unit := session.ServiceUnit(out)
	if unit == "" {
		return nil, nil
	}
	return &TmuxUnit{Pid: pid, Unit: unit, Reason: fmt.Sprintf(
		"tmux server %d runs inside %s, so stopping or restarting that unit ends every window; once its sessions are finished, restart it with `%s`, and engine starts the next one in a scope of its own",
		pid, unit, d.killServer())}, nil
}
