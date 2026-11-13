package session

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTitle(t *testing.T) {
	long := strings.Repeat("word ", 20)
	tests := []struct {
		name, id, title, want string
	}{
		{"a task", "t63", "Retry Codex thread labels", "t63 · Retry Codex thread labels"},
		{"a plan", "p12", "Session titles", "p12 · Session titles"},
		{"no title is the ID", "t63", "  ", "t63"},
		{"whitespace and control characters fold", "t63", "Fix\n\tthe\x07  tab ", "t63 · Fix the tab"},
		{"a long title is cut", "t63", long, "t63 · word word word word word word word word word word wor…"},
		{"a cut never ends in a space", "t63", strings.Repeat("ab ", 30), "t63 · ab ab ab ab ab ab ab ab ab ab ab ab ab ab ab ab ab ab…"},
	}
	for _, tt := range tests {
		got := Title(tt.id, tt.title)
		if got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
		if n := utf8.RuneCountInString(got); n > TitleMax {
			t.Errorf("%s: %d characters, more than %d", tt.name, n, TitleMax)
		}
	}
}

func TestDisplayTitle(t *testing.T) {
	if got := (Record{Name: "r-7", Title: "t7 · Fix it"}).DisplayTitle(); got != "t7 · Fix it" {
		t.Errorf("titled run: %q", got)
	}
	if got := (Record{Name: "r-7"}).DisplayTitle(); got != "r-7" {
		t.Errorf("untitled run: %q", got)
	}
}
