package session

import (
	"errors"
	"strings"
	"testing"
)

func TestKeepAwakeAsksTmuxToRunCaffeinateOnThePanePid(t *testing.T) {
	tm, f := newFake(t)
	tm.Awake = "/usr/bin/caffeinate"
	got, err := tm.Open(window(t))
	if err != nil || got.AwakeErr != nil {
		t.Fatalf("got %+v, %v", got, err)
	}
	last := f.calls[len(f.calls)-1]
	want := `tmux run-shell -b -t %1 pid=#{pane_pid}; [ -z "$pid" ] || '/usr/bin/caffeinate' -s -w "$pid" >/dev/null 2>&1; exit 0`
	if last != want {
		t.Errorf("last call:\n%s\nwant:\n%s", last, want)
	}
}

func TestKeepAwakeIsOffWithoutACaffeinate(t *testing.T) {
	tm, f := newFake(t)
	if _, err := tm.Open(window(t)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.dump(), "run-shell") {
		t.Errorf("caffeinate was started:\n%s", f.dump())
	}
}

func TestKeepAwakeFailureDoesNotCloseTheWindow(t *testing.T) {
	tm, f := newFake(t)
	tm.Awake = "caffeinate"
	f.answer("run-shell", "", errors.New("no shell"))
	got, err := tm.Open(window(t))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	if got.AwakeErr == nil || !strings.Contains(got.AwakeErr.Error(), "keeping the host awake") {
		t.Errorf("AwakeErr = %v", got.AwakeErr)
	}
	if strings.Contains(f.dump(), "kill-window") {
		t.Error("the window was killed")
	}
}

func TestKeepAwakeEscapesFormatCharactersInThePath(t *testing.T) {
	tm, f := newFake(t)
	tm.Awake = "/opt/a#b/caffeinate"
	if _, err := tm.Open(window(t)); err != nil {
		t.Fatal(err)
	}
	if last := f.calls[len(f.calls)-1]; !strings.Contains(last, `|| '/opt/a##b/caffeinate'`) {
		t.Errorf("got %s", last)
	}
}
