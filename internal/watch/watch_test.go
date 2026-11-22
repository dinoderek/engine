package watch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var start = time.Date(2026, 11, 22, 17, 2, 0, 0, time.UTC)

// rig is a watcher with a fake server and a fake bot.
type rig struct {
	w    *Watcher
	up   bool
	sent []string
	// sendErr fails every send while set.
	sendErr error
	now     time.Time
}

func newRig(t *testing.T) *rig {
	r := &rig{up: true, now: start}
	r.w = &Watcher{
		Host: "devbox",
		Probe: func(ctx context.Context) error {
			if _, ok := ctx.Deadline(); !ok {
				t.Error("the probe has no deadline")
			}
			if r.up {
				return nil
			}
			return errors.New("context deadline exceeded")
		},
		Send: func(_ context.Context, text string) error {
			if r.sendErr != nil {
				return r.sendErr
			}
			r.sent = append(r.sent, text)
			return nil
		},
		Now:       func() time.Time { return r.now },
		StatePath: filepath.Join(t.TempDir(), "share", "watch.json"),
	}
	return r
}

// pass runs one pass a minute after the last.
func (r *rig) pass(t *testing.T) error {
	t.Helper()
	err := r.w.Pass(context.Background())
	r.now = r.now.Add(Interval)
	return err
}

func (r *rig) passes(t *testing.T, n int) {
	t.Helper()
	for range n {
		if err := r.pass(t); err != nil {
			t.Fatal(err)
		}
	}
}

const wantDown = "Engine server on devbox is not answering since 2026-11-22 17:03 UTC.\n" +
	"Diagnose it: engine server status\nRestart it: engine server restart"

func TestOneFailureSendsNothing(t *testing.T) {
	r := newRig(t)
	r.passes(t, 1)
	r.up = false
	r.passes(t, 1)
	r.up = true
	r.passes(t, 2)
	if len(r.sent) != 0 {
		t.Errorf("sent %q", r.sent)
	}
}

func TestTwoFailuresInARowSendOneMessageWithTheCommands(t *testing.T) {
	r := newRig(t)
	r.passes(t, 1)
	r.up = false
	r.passes(t, 2)
	if len(r.sent) != 1 || r.sent[0] != wantDown {
		t.Fatalf("sent %q, want %q", r.sent, wantDown)
	}
	r.passes(t, 5)
	if len(r.sent) != 1 {
		t.Errorf("more failures sent more: %q", r.sent)
	}
	r.up = true
	r.passes(t, 3)
	want := "Engine server on devbox answers again; it was not answering since 2026-11-22 17:03 UTC."
	if len(r.sent) != 2 || r.sent[1] != want {
		t.Errorf("recovery sent %q, want one more: %q", r.sent, want)
	}
}

func TestFailuresApartSendNothing(t *testing.T) {
	r := newRig(t)
	for range 3 {
		r.up = false
		r.passes(t, 1)
		r.up = true
		r.passes(t, 1)
	}
	if len(r.sent) != 0 {
		t.Errorf("sent %q", r.sent)
	}
}

func TestAFailedSendIsRetriedNextPass(t *testing.T) {
	r := newRig(t)
	r.up, r.sendErr = false, errors.New("telegram request failed")
	r.passes(t, 1)
	if err := r.pass(t); err == nil || !strings.Contains(err.Error(), "send the Telegram message: telegram request failed") {
		t.Fatalf("pass error %v", err)
	}
	r.sendErr = nil
	r.passes(t, 1)
	if len(r.sent) != 1 || r.sent[0] != strings.Replace(wantDown, "17:03", "17:02", 1) {
		t.Fatalf("sent %q", r.sent)
	}
	r.up, r.sendErr = true, errors.New("telegram request failed")
	if err := r.pass(t); err == nil {
		t.Fatal("a failed recovery send was not reported")
	}
	r.sendErr = nil
	r.passes(t, 2)
	if len(r.sent) != 2 || !strings.Contains(r.sent[1], "answers again") {
		t.Errorf("recovery sent %q", r.sent)
	}
}

func TestTheStreakSurvivesARestartOfTheWatcher(t *testing.T) {
	r := newRig(t)
	r.up = false
	r.passes(t, 1)
	restarted := *r.w
	r.w = &restarted
	r.passes(t, 1)
	if len(r.sent) != 1 {
		t.Fatalf("a failure before the restart was forgotten: %q", r.sent)
	}
	again := *r.w
	r.w = &again
	r.passes(t, 3)
	if len(r.sent) != 1 {
		t.Errorf("a restart repeated the alert: %q", r.sent)
	}
	info, err := os.Stat(r.w.StatePath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("state file %v, %v", info, err)
	}
}

func TestAStateFileThatDoesNotReadStartsAfresh(t *testing.T) {
	r := newRig(t)
	if err := os.MkdirAll(filepath.Dir(r.w.StatePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.w.StatePath, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.up = false
	if err := r.pass(t); err == nil || !strings.Contains(err.Error(), "starting afresh") {
		t.Fatalf("pass error %v", err)
	}
	r.passes(t, 1)
	if len(r.sent) != 1 {
		t.Errorf("sent %q", r.sent)
	}
}

func TestAStateFileThatCannotBeWrittenIsAnError(t *testing.T) {
	r := newRig(t)
	r.w.StatePath = filepath.Join(t.TempDir(), "file", "watch.json")
	if err := os.WriteFile(filepath.Dir(r.w.StatePath), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.pass(t); err == nil {
		t.Error("a state that cannot be saved passed")
	}
}

func TestRunPassesUntilTheContextEnds(t *testing.T) {
	r := newRig(t)
	r.up = false
	var logged []string
	r.w.Logf = func(format string, args ...any) { logged = append(logged, format) }
	r.sendErr = errors.New("telegram request failed")
	ctx, cancel := context.WithCancel(context.Background())
	waits := 0
	r.w.Run(ctx, func(_ context.Context, d time.Duration) bool {
		if d != Interval {
			t.Errorf("waited %s", d)
		}
		waits++
		if waits == 3 {
			cancel()
			return false
		}
		return true
	})
	if waits != 3 || len(logged) != 5 {
		t.Errorf("waits %d, logged %q", waits, logged)
	}
}

func TestSleepEndsWithTheContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if sleep(ctx, time.Hour) {
		t.Error("a cancelled sleep finished")
	}
	if !sleep(context.Background(), 0) {
		t.Error("a zero sleep did not finish")
	}
}

func TestEachFailedCallIsLoggedWithWhy(t *testing.T) {
	r := newRig(t)
	var logged []string
	r.w.Logf = func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) }
	r.up = false
	r.passes(t, 2)
	r.up = true
	r.passes(t, 1)
	want := "engine watch: the server did not answer: context deadline exceeded"
	if len(logged) != 2 || logged[0] != want {
		t.Errorf("logged %q, want two of %q", logged, want)
	}
}

// An outage that follows an unsent recovery keeps the first outage's time,
// and the recovery that finally goes out says so.
func TestAnOutageAfterAnUnsentRecoveryKeepsItsSince(t *testing.T) {
	r := newRig(t)
	r.up = false
	r.passes(t, 2)
	r.up, r.sendErr = true, errors.New("telegram request failed")
	_ = r.pass(t)
	r.up = false
	_ = r.pass(t)
	_ = r.pass(t)
	r.up, r.sendErr = true, nil
	r.passes(t, 1)
	want := "Engine server on devbox answers again; it was not answering since 2026-11-22 17:02 UTC."
	if len(r.sent) != 2 || r.sent[1] != want {
		t.Errorf("sent %q, want the recovery %q", r.sent, want)
	}
}

// A failure saved before the Mac slept does not pair with the first one after
// it wakes.
func TestAStaleFailureStartsAFreshStreak(t *testing.T) {
	r := newRig(t)
	r.up = false
	r.passes(t, 1)
	r.now = r.now.Add(8 * time.Hour)
	r.passes(t, 1)
	if len(r.sent) != 0 {
		t.Fatalf("a stale failure counted: %q", r.sent)
	}
	r.passes(t, 1)
	if len(r.sent) != 1 || !strings.Contains(r.sent[0], "since 2026-11-23 01:03 UTC") {
		t.Errorf("sent %q", r.sent)
	}
}
