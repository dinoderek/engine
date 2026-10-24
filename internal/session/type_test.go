package session

import (
	"errors"
	"testing"
	"time"
)

func TestTypeSendsTextThenEnter(t *testing.T) {
	f := &fake{}
	var slept time.Duration
	tm := &Tmux{Run: f.run, Bin: "tmux", Sleep: func(d time.Duration) { slept = d }}
	if err := tm.Type("@4", "read it; now"); err != nil {
		t.Fatal(err)
	}
	want := "tmux send-keys -t =engine:@4 -l read it; now\ntmux send-keys -t =engine:@4 Enter"
	if f.dump() != want || slept != enterDelay {
		t.Errorf("calls\n%s\nslept %v", f.dump(), slept)
	}
}

func TestTypeStopsWhenTheTextFails(t *testing.T) {
	f := &fake{}
	f.answer("send-keys", "", errors.New("no such window"))
	tm := &Tmux{Run: f.run, Bin: "tmux", Sleep: func(time.Duration) { t.Error("slept after a failure") }}
	if err := tm.Type("@4", "x"); err == nil || len(f.calls) != 1 {
		t.Errorf("err %v, calls %q", err, f.calls)
	}
}
