package controlcmd

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/dinoderek/engine/internal/hosts"
)

// startSystemd starts the server through the systemd unit h records, instead
// of a tmux window: a unit already active is left as it is, and a unit that is
// not active a moment after its start fails with where to read why.
func startSystemd(e Env, d ServerDeps, h hosts.Host) error {
	_, origin := hosts.ServerAddress(h)
	unit := h.Services.Server
	active, err := unitActive(d, h)
	if err != nil {
		return err
	}
	if active {
		fmt.Fprintf(e.Out, "engine server already running on %s at %s (systemd unit %s)\n", h.Name, origin, unit)
		return nil
	}
	out, code, err := systemctl(d, h, hosts.ServerStart)
	switch {
	case err != nil:
		return err
	case code != 0:
		return fmt.Errorf("systemctl --user start %s failed (exit %d):\n%s\n%s", unit, code, out, journalHint(unit))
	}
	d.Sleep(serverSettle)
	if active, err = unitActive(d, h); err != nil {
		return err
	}
	if !active {
		return fmt.Errorf("the server unit %s is not active after its start\n%s", unit, journalHint(unit))
	}
	fmt.Fprintf(e.Out, "engine server started on %s at %s (systemd unit %s)\n", h.Name, origin, unit)
	return nil
}

func journalHint(unit string) string {
	return "read the unit's log with: journalctl --user -u " + unit
}

// unitActive is whether systemctl reports the server's unit active; any
// other answer, an unknown unit included, is "not active".
func unitActive(d ServerDeps, h hosts.Host) (bool, error) {
	_, code, err := systemctl(d, h, hosts.ServerIsActive)
	return err == nil && code == 0, err
}

// systemctl runs op on the server's unit on this host and returns its output
// and exit code.
func systemctl(d ServerDeps, h hosts.Host, op hosts.ServerOp) (string, int, error) {
	argv, err := h.ServerCommand(op)
	if err != nil {
		return "", -1, err
	}
	var buf bytes.Buffer
	code, err := d.Exec(nil, &buf, &buf, argv[0], argv[1:]...)
	return strings.TrimSpace(buf.String()), code, err
}
