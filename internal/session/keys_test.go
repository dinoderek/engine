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
