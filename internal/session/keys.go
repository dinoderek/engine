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
// it cannot find is typed into as before. Before it sends anything, it
// passes what it will submit to before, when set; an error sends nothing.
func (t *Tmux) Submit(id, text string, before func(string) error) error {
	target := sessionTarget + id
	screen, err := t.tmux("capture-pane", "-p", "-e", "-t", target)
	if err != nil {
		return err
	}
	unsent, ok := InputLine(screen)
	typing := !ok || unsent == ""
	if typing {
		unsent = text
	}
	if before != nil {
		if err := before(unsent); err != nil {
			return err
		}
	}
	if typing {
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

// InputLine is the text on the input line of a screen, the visible pane
// captured with its escape sequences: the lowest line that starts, inside
// any box border, with a prompt mark and sits right under a rule. Faint
// text, such as a placeholder or a suggestion, is not typed text and is
// dropped. It reports false when no line does.
func InputLine(screen string) (string, bool) {
	lines := strings.Split(strings.TrimRight(screen, "\n"), "\n")
	for i, line := range lines {
		lines[i] = typed(line)
	}
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

// typed is a captured line without its escape sequences and without the
// text they render faint (SGR 2, until 0 or 22).
func typed(line string) string {
	var b strings.Builder
	faint := false
	for i := 0; i < len(line); i++ {
		if line[i] != 0x1b {
			if !faint {
				b.WriteByte(line[i])
			}
			continue
		}
		if i+1 < len(line) && strings.IndexByte("()*+#", line[i+1]) >= 0 {
			i += 2 // a character set designation, such as ESC ( B
			continue
		}
		if i+1 >= len(line) || line[i+1] != '[' {
			i++ // a two-byte escape
			continue
		}
		end := i + 2
		for end < len(line) && (line[end] < 0x40 || line[end] > 0x7e) {
			end++
		}
		if end < len(line) && line[end] == 'm' {
			faint = sgrFaint(line[i+2:end], faint)
		}
		i = end
	}
	return b.String()
}

// sgrFaint is whether text is faint after the SGR parameters params, given
// whether it was before. Colour arguments (38;5;n, 38;2;r;g;b) are skipped.
func sgrFaint(params string, faint bool) bool {
	ps := strings.Split(params, ";")
	for i := 0; i < len(ps); i++ {
		switch ps[i] {
		case "", "0", "22":
			faint = false
		case "2":
			faint = true
		case "38", "48", "58":
			if i+1 < len(ps) && ps[i+1] == "5" {
				i += 2
			} else if i+1 < len(ps) && ps[i+1] == "2" {
				i += 4
			}
		}
	}
	return faint
}
