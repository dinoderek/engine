package session

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestTypeSendsTextThenEnter(t *testing.T) {
	f := &fake{}
	f.answer("display-message", "0\n", nil)
	var slept time.Duration
	tm := &Tmux{Run: f.run, Bin: "tmux", Sleep: func(d time.Duration) { slept = d }}
	if err := tm.Type("@4", "read it; now"); err != nil {
		t.Fatal(err)
	}
	want := "tmux display-message -p -t =engine:@4 #{pane_in_mode}\n" +
		"tmux send-keys -t =engine:@4 -l read it; now\ntmux send-keys -t =engine:@4 Enter"
	if f.dump() != want || slept != enterDelay {
		t.Errorf("calls\n%s\nslept %v", f.dump(), slept)
	}
}

func TestTypeLeavesCopyModeFirst(t *testing.T) {
	f := &fake{}
	f.answer("display-message", "1\n", nil)
	tm := &Tmux{Run: f.run, Bin: "tmux", Sleep: func(time.Duration) {}}
	if err := tm.Type("@4", "x"); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 4 || f.calls[1] != "tmux send-keys -X -t =engine:@4 cancel" {
		t.Errorf("calls\n%s", f.dump())
	}
}

func TestTypeFailures(t *testing.T) {
	for _, sub := range []string{"display-message", "send-keys"} {
		f := &fake{}
		f.answer("display-message", "1\n", nil)
		f.answer(sub, "", errors.New("no such window"))
		tm := &Tmux{Run: f.run, Bin: "tmux", Sleep: func(time.Duration) { t.Error("slept after a failure") }}
		if err := tm.Type("@4", "x"); err == nil {
			t.Errorf("%s failing: no error; calls %q", sub, f.calls)
		}
	}
}

func TestCaptureAndKill(t *testing.T) {
	f := &fake{}
	f.answer("capture-pane", "line one\nDo you want to proceed?\n\n\n", nil)
	tm := &Tmux{Run: f.run, Bin: "tmux"}
	got, err := tm.Capture("@4", 20)
	if err != nil || got != "line one\nDo you want to proceed?" {
		t.Errorf("capture %q, %v", got, err)
	}
	if err := tm.Kill("@4"); err != nil {
		t.Fatal(err)
	}
	want := "tmux capture-pane -p -t =engine:@4 -S -20\ntmux kill-window -t =engine:@4"
	if f.dump() != want {
		t.Errorf("calls\n%s", f.dump())
	}
}

func TestInputLine(t *testing.T) {
	for _, tc := range []struct {
		name, screen, text string
		found              bool
	}{
		{"empty", "output\n──── t1 · title ─\n❯ \n────\n  engine-1  5h 81%\n", "", true},
		{"unsent", "output\n────────\n❯ continue\n────────\n  ⏵⏵ auto mode on\n", "continue", true},
		{"boxed", "╭──────╮\n│ > continue   │\n╰──────╯\n", "continue", true},
		{"lowest wins", "────\n❯ old\n────\n❯ \n────\n", "", true},
		{"quoted prompt", "> not an input line\nmore\n", "", false},
		{"no box", "just output\n", "", false},
		{"faint placeholder", "────\n\x1b[38;5;246m❯ \x1b[39m\x1b[2mTry \"fix lint\"\x1b[22m\n────\n", "", true},
		{"coloured text", "\x1b[38;5;244m────\x1b[39m\n\x1b[38;2;2;2;2m❯ continue\x1b[0m\n", "continue", true},
		{"faint reset", "────\n❯ \x1b[2mhint\x1b[0m sent\x1b(B\n", "sent", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text, found := InputLine(tc.screen)
			if text != tc.text || found != tc.found {
				t.Errorf("got %q, %v; want %q, %v", text, found, tc.text, tc.found)
			}
		})
	}
}

func TestSubmitSendsUnsentTextWithEnterAlone(t *testing.T) {
	f := &fake{}
	f.answer("capture-pane", "work\n────────\n❯ go on please\n────────\n", nil)
	f.answer("display-message", "0\n", nil)
	tm := &Tmux{Run: f.run, Bin: "tmux", Sleep: func(time.Duration) { t.Error("waited before a lone Enter") }}
	var logged []string
	if err := tm.Submit("@4", "continue", func(s string) error { logged = append(logged, s); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(logged) != 1 || logged[0] != "go on please" {
		t.Errorf("before got %q, want the unsent text it submits", logged)
	}
	want := "tmux capture-pane -p -e -t =engine:@4\n" +
		"tmux display-message -p -t =engine:@4 #{pane_in_mode}\ntmux send-keys -t =engine:@4 Enter"
	if f.dump() != want {
		t.Errorf("calls\n%s", f.dump())
	}
}

func TestSubmitTypesIntoAnEmptyInputLine(t *testing.T) {
	for _, screen := range []string{"work\n────────\n❯ \n────────\n", "a screen with no input box\n"} {
		f := &fake{}
		f.answer("capture-pane", screen, nil)
		f.answer("display-message", "0\n", nil)
		tm := &Tmux{Run: f.run, Bin: "tmux", Sleep: func(time.Duration) {}}
		var logged []string
		if err := tm.Submit("@4", "continue", func(s string) error { logged = append(logged, s); return nil }); err != nil {
			t.Fatal(err)
		}
		if len(logged) != 1 || logged[0] != "continue" {
			t.Errorf("screen %q: before got %q, want the text it types", screen, logged)
		}
		want := "tmux capture-pane -p -e -t =engine:@4\n" +
			"tmux display-message -p -t =engine:@4 #{pane_in_mode}\n" +
			"tmux send-keys -t =engine:@4 -l continue\ntmux send-keys -t =engine:@4 Enter"
		if f.dump() != want {
			t.Errorf("screen %q: calls\n%s", screen, f.dump())
		}
	}
}

func TestSubmitFailures(t *testing.T) {
	for _, sub := range []string{"capture-pane", "display-message", "send-keys"} {
		f := &fake{}
		f.answer("capture-pane", "────\n❯ continue\n", nil)
		f.answer("display-message", "1\n", nil)
		f.answer(sub, "", errors.New("no such window"))
		tm := &Tmux{Run: f.run, Bin: "tmux", Sleep: func(time.Duration) {}}
		if err := tm.Submit("@4", "continue", nil); err == nil {
			t.Errorf("%s failing: no error; calls %q", sub, f.calls)
		}
	}
	f := &fake{}
	f.answer("capture-pane", "────\n❯ \n", nil)
	tm := &Tmux{Run: f.run, Bin: "tmux", Sleep: func(time.Duration) {}}
	if err := tm.Submit("@4", "continue", func(string) error { return errors.New("not logged") }); err == nil || strings.Contains(f.dump(), "send-keys") {
		t.Errorf("a text before refused was sent: %v, calls\n%s", err, f.dump())
	}
}
