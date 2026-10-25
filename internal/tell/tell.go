// Package tell posts a message on a task's issue and points the task's live
// session at it. The comment is the record; the typed pointer makes the
// session read it now rather than at its next inbox check.
package tell

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/tracker"
)

// Deps are the programs and the environment tell reaches.
type Deps struct {
	// Gh runs gh as the machine user.
	Gh     tracker.Runner
	Tmux   *session.Tmux
	Getenv func(string) string
	Home   string
}

// Told is what Tell did.
type Told struct {
	Task tracker.Ref
	// URL is the comment's web address.
	URL string
	// Window is the tmux window the pointer was typed into.
	Window string
}

// Tell comments text on the issue of the task whose session is named name,
// then types a pointer to the comment into the task's tmux window on this
// host. A session on another host, or one with no window, keeps the comment
// and gets an error: it reads the comment at its next inbox check.
func Tell(name, text string, d Deps) (Told, error) {
	token, err := tracker.Token(d.Getenv, d.Home)
	if err != nil {
		return Told{}, err
	}
	tasks, err := tracker.Claimed(d.Gh, token)
	if err != nil {
		return Told{}, err
	}
	task, ok := tracker.FindClaim(tasks, name)
	if !ok {
		return Told{}, fmt.Errorf("no claimed task named %s", name)
	}
	if task.Claim.Host == "" {
		return Told{}, fmt.Errorf("the claim of %s names no host", name)
	}
	_, local, err := hosts.Open(hosts.ConfigDir(d.Home))
	if err != nil {
		return Told{}, err
	}
	told := Told{Task: task.Ref}
	if told.URL, err = tracker.Comment(d.Gh, token, task.Ref, text); err != nil {
		return told, err
	}
	if task.Claim.Host != local.Name {
		return told, fmt.Errorf("commented at %s, but %s runs on %q, not this host (%q): run engine tell there to type the pointer", told.URL, name, task.Claim.Host, local.Name)
	}
	id, err := window(d.Tmux, name)
	if err != nil {
		return told, fmt.Errorf("commented at %s, but %w", told.URL, err)
	}
	if err := d.Tmux.Type(id, Pointer(told.URL)); err != nil {
		return told, fmt.Errorf("commented at %s, but typing into the window failed: %w", told.URL, err)
	}
	told.Window = session.Name + ":" + id
	return told, nil
}

// Text is the comment text for the words after the task name. A lone "-" reads
// the text from in, so a long or multi-line answer needs no shell quoting;
// otherwise the words are joined with spaces. Empty text is refused.
func Text(words []string, in io.Reader) (string, error) {
	text := strings.Join(words, " ")
	if len(words) == 1 && words[0] == "-" {
		data, err := io.ReadAll(in)
		if err != nil {
			return "", fmt.Errorf("read the text from stdin: %w", err)
		}
		text = strings.TrimRight(string(data), " \t\r\n")
	}
	if strings.TrimSpace(text) == "" {
		return "", errors.New("the text is empty")
	}
	return text, nil
}

// Pointer is the line typed into the session.
func Pointer(url string) string {
	return "New comment on your task's issue, read it and act on it now: " + url
}

func window(tm *session.Tmux, name string) (string, error) {
	windows, err := tm.Windows()
	if err != nil {
		return "", err
	}
	for _, w := range windows {
		if w.Name == name && !w.Dead {
			return w.ID, nil
		}
	}
	return "", fmt.Errorf("no live tmux window named %s in the session %s", name, session.Name)
}
