package session

import (
	"strconv"
	"strings"
	"time"
)

// enterDelay is how long Type waits before Enter: an input box can swallow
// a newline that arrives with the text.
const enterDelay = 500 * time.Millisecond

// Type types text into the window with id, then presses Enter as a
// separate key, after a pause. A pane in copy or view mode, as after a
// scroll with the mouse, would take the keys as its own commands, so Type
// leaves the mode first.
func (t *Tmux) Type(id, text string) error {
	target := sessionTarget + id
	mode, err := t.tmux("display-message", "-p", "-t", target, "#{pane_in_mode}")
	if err != nil {
		return err
	}
	if strings.TrimSpace(mode) == "1" {
		if _, err := t.tmux("send-keys", "-X", "-t", target, "cancel"); err != nil {
			return err
		}
	}
	if _, err := t.tmux("send-keys", "-t", target, "-l", text); err != nil {
		return err
	}
	sleep := t.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	sleep(enterDelay)
	_, err = t.tmux("send-keys", "-t", target, "Enter")
	return err
}

// Capture returns the last n lines of the screen of the window with id,
// trailing blank lines dropped.
func (t *Tmux) Capture(id string, n int) (string, error) {
	out, err := t.tmux("capture-pane", "-p", "-t", sessionTarget+id, "-S", "-"+strconv.Itoa(n))
	return strings.TrimRight(out, "\n "), err
}

// Kill closes the window with id.
func (t *Tmux) Kill(id string) error {
	_, err := t.tmux("kill-window", "-t", sessionTarget+id)
	return err
}
