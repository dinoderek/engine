package session

import (
	"errors"
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
	f.answer("capture-pane", "work\n────────\n❯ continue\n────────\n", nil)
	f.answer("display-message", "0\n", nil)
	tm := &Tmux{Run: f.run, Bin: "tmux", Sleep: func(time.Duration) { t.Error("waited before a lone Enter") }}
	if err := tm.Submit("@4", "continue"); err != nil {
		t.Fatal(err)
	}
	want := "tmux capture-pane -p -t =engine:@4\n" +
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
		if err := tm.Submit("@4", "continue"); err != nil {
			t.Fatal(err)
		}
		want := "tmux capture-pane -p -t =engine:@4\n" +
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
		if err := tm.Submit("@4", "continue"); err == nil {
			t.Errorf("%s failing: no error; calls %q", sub, f.calls)
		}
	}
}
