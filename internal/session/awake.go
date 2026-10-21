package session

import "fmt"

// keepAwake holds a macOS host awake for as long as the command in pane runs:
// tmux starts `caffeinate -s -w <pid>` in the background, and caffeinate
// exits when that process does. Each window brings its own, so the host may
// sleep once the last command has ended, and nothing outlives the tmux server
// or its windows. `-s` holds off system sleep while on power. It does
// nothing when t.Awake is empty.
func (t *Tmux) keepAwake(pane string) error {
	if t.Awake == "" {
		return nil
	}
	if _, err := t.tmux("run-shell", "-b", "-t", pane, awakeScript(t.Awake)); err != nil {
		return fmt.Errorf("keeping the host awake: %w", err)
	}
	return nil
}

// awakeScript is the job tmux runs for a pane. A pane whose command has
// already exited has no pid: nothing to hold. The job must neither print nor
// exit non-zero: run-shell shows either in the target pane, which puts the
// agent's pane in view mode, where it swallows send-keys.
func awakeScript(bin string) string {
	return `pid=#{pane_pid}; [ -z "$pid" ] || ` + shellQuote(formatEscape(bin)) + ` -s -w "$pid" >/dev/null 2>&1; exit 0`
}
