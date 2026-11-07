package hostops

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/dinoderek/engine/internal/session"
)

// daemonEnvironment refuses to join a process whose host environment setup
// has not recorded. Embedded sessions use the caller's environment instead.
func (c codex) daemonEnvironment(run session.Runner) string {
	out, err := run("ps", "-ww", "-eo", "pid=,lstart=,args=")
	if err != nil {
		return "cannot identify engine's Codex daemon (" + err.Error() + ")"
	}
	instance := ""
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 7 && remoteControlOn(strings.Join(fields[6:], " "), c.engineHome()) {
			if instance != "" {
				return "engine's Codex daemon identity is ambiguous"
			}
			instance = strings.Join(fields[:6], " ")
		}
	}
	receipt, err := os.ReadFile(filepath.Join(c.engineHome(), "daemon-environment"))
	if err != nil || instance == "" || strings.TrimSpace(string(receipt)) != instance {
		return "engine's Codex daemon needs a host environment refresh; run `engine setup` on this host"
	}
	return ""
}
