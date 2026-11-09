package reconcile

import (
	"strings"
	"testing"
	"time"
)

// Only an idle session takes a pointer: one waiting for input, or one that
// only a background job keeps busy. A session busy in a turn, limited,
// unknown or gone, or a task at a hard stop, is left alone.
func TestWakeable(t *testing.T) {
	dead := gone()
	unknown := Session{Unknown: true, Waiting: true}
	limitedWait := waiting(time.Minute)
	limitedWait.Limited = true
	for _, tc := range []struct {
		name string
		t    Item
		want bool
	}{
		{"waiting", with(task(2, "running"), waiting(time.Minute)), true},
		{"busy only in a background job", with(task(2, "in-review"), background(time.Minute)), true},
		{"a done task busy only in a background job", with(task(2, "done"), background(0)), true},
		{"busy in a turn", with(task(2, "running"), working()), false},
		{"limited", with(task(2, "running"), limitedWait), false},
		{"unknown", with(task(2, "running"), unknown), false},
		{"gone", with(task(2, "running"), dead), false},
		{"no session", task(2, "running"), false},
		{"at a hard stop", with(task(2, "needs-human"), background(time.Minute)), false},
		{"with an open question", question(with(task(2, "running"), waiting(time.Minute)), 7), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.t.Wakeable(); got != tc.want {
				t.Errorf("Wakeable() = %v, want %v", got, tc.want)
			}
		})
	}
}

// A background spell counts toward a done task's release, but never toward
// a stall: Waited is for waiting only.
func TestIdleSpells(t *testing.T) {
	bg := background(StallWait)
	if !bg.IdleFor(t0, StallWait) || bg.Waited(t0, StallWait) {
		t.Errorf("background: IdleFor %v, Waited %v; want true, false", bg.IdleFor(t0, StallWait), bg.Waited(t0, StallWait))
	}
	w := waiting(StallWait)
	if !w.IdleFor(t0, StallWait) || !w.Waited(t0, StallWait) {
		t.Errorf("waiting: IdleFor %v, Waited %v; want true, true", w.IdleFor(t0, StallWait), w.Waited(t0, StallWait))
	}
	if short := background(StallWait - time.Second); short.IdleFor(t0, StallWait) {
		t.Error("a short background spell counted as StallWait")
	}
	reset := waiting(StallWait)
	reset.ResetsAt = unix(time.Hour)
	if reset.IdleFor(t0, StallWait) {
		t.Error("a wait on a limit reset counted as idle")
	}
}

// Releasing a session that only a background job keeps busy tells the
// operator, once per attempt; releasing a waiting one does not.
func TestReleaseNoticesARunningBackgroundJob(t *testing.T) {
	acts := Decide(view(plan("executing"), with(task(2, "done"), background(StallWait)), with(task(3, "done"), waiting(StallWait))))
	if len(acts) != 3 {
		t.Fatalf("actions:\n%s", shows(acts))
	}
	bg, plain := acts[0], acts[1]
	if bg.Key != "released/a2" || !strings.Contains(bg.Text, "demo-2 was released, with a background job still running, because its task is done") {
		t.Errorf("background release %+v", bg)
	}
	if plain.Key != "" || plain.Text != "its task is done" {
		t.Errorf("waiting release %+v", plain)
	}
}
