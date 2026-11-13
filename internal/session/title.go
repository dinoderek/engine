package session

import (
	"strings"
	"unicode"
)

// TitleMax is the most characters a session title keeps: the apps' session
// lists cut a longer one anyway.
const TitleMax = 60

// Title is how the harness apps show the session of a server item: its ID
// and its title, such as "t63 · Retry Codex thread labels". The apps already
// group sessions by repo, so the title leaves the repo out.
func Title(id, title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return CleanTitle(id)
	}
	return CleanTitle(id + " · " + title)
}

// CleanTitle makes s fit a session title: each run of spaces and control
// characters becomes one space, and a title longer than TitleMax is cut,
// ending with an ellipsis.
func CleanTitle(s string) string {
	s = strings.Join(strings.FieldsFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}), " ")
	runes := []rune(s)
	if len(runes) <= TitleMax {
		return s
	}
	return strings.TrimRightFunc(string(runes[:TitleMax-1]), unicode.IsSpace) + "…"
}

// DisplayTitle is the title the apps show for the run: its recorded title,
// or its session name for a run that has none.
func (r Record) DisplayTitle() string {
	if r.Title != "" {
		return r.Title
	}
	return r.Name
}
