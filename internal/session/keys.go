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
	if err := t.leaveMode(target); err != nil {
		return err
	}
	if _, err := t.tmux("send-keys", "-t", target, "-l", text); err != nil {
		return err
	}
	sleep := t.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	sleep(enterDelay)
	_, err := t.tmux("send-keys", "-t", target, "Enter")
	return err
}

// Submit sends text as Type does, unless the window's input line already
// holds text, as when the harness typed its own continue and never sent it:
// then it presses Enter alone, which sends that. A screen whose input line
// it cannot find is typed into as before.
func (t *Tmux) Submit(id, text string) error {
	target := sessionTarget + id
	screen, err := t.tmux("capture-pane", "-p", "-t", target)
	if err != nil {
		return err
	}
	if unsent, ok := InputLine(screen); !ok || unsent == "" {
		return t.Type(id, text)
	}
	if err := t.leaveMode(target); err != nil {
		return err
	}
	_, err = t.tmux("send-keys", "-t", target, "Enter")
	return err
}

// leaveMode takes the pane out of copy or view mode, if it is in one.
func (t *Tmux) leaveMode(target string) error {
	mode, err := t.tmux("display-message", "-p", "-t", target, "#{pane_in_mode}")
	if err != nil {
		return err
	}
	if strings.TrimSpace(mode) == "1" {
		_, err = t.tmux("send-keys", "-X", "-t", target, "cancel")
	}
	return err
}

// promptMarks are the marks an input line starts with: the agent CLIs draw
// their input box as a rule, then the prompt mark and what is typed.
var promptMarks = []string{"❯", ">", "›"}

// InputLine is the text on the input line of a screen, the visible pane:
// the lowest line that starts, inside any box border, with a prompt mark
// and sits right under a rule. It reports false when no line does.
func InputLine(screen string) (string, bool) {
	lines := strings.Split(strings.TrimRight(screen, "\n"), "\n")
	for i := len(lines) - 1; i > 0; i-- {
		above := strings.TrimSpace(lines[i-1])
		if !strings.HasPrefix(above, "─") && !strings.HasPrefix(above, "╭") {
			continue
		}
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[i]), "│"))
		for _, mark := range promptMarks {
			if rest, ok := strings.CutPrefix(line, mark); ok {
				return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rest), "│")), true
			}
		}
	}
	return "", false
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
