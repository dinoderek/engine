package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// pwd runs `pwd -P` through run and returns the directory it printed.
func pwd(t *testing.T, run Runner) string {
	t.Helper()
	out, err := run("pwd", "-P")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out)
}

func resolve(t *testing.T, dir string) string {
	t.Helper()
	p, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// A tmux server starts in the directory of the process that first runs it, so
// every runner tmux is driven through runs from the home directory.
func TestRunnersRunFromTheirDirectory(t *testing.T) {
	dir := t.TempDir()
	if got := pwd(t, ExecIn(dir)); got != resolve(t, dir) {
		t.Errorf("ExecIn ran in %s, want %s", got, dir)
	}
	if got := pwd(t, ExecContextIn(context.Background(), time.Minute, dir)); got != resolve(t, dir) {
		t.Errorf("ExecContextIn ran in %s, want %s", got, dir)
	}
}

func TestRunnersWithoutADirectoryRefuse(t *testing.T) {
	for name, run := range map[string]Runner{"ExecIn": ExecIn(""), "ExecContextIn": ExecContextIn(context.Background(), time.Minute, "")} {
		if _, err := run("pwd"); err == nil || !strings.Contains(err.Error(), "no home directory") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestNewRunsTmuxFromTheHomeDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	tm := New()
	if tm.Home != home {
		t.Errorf("home %q, want %q", tm.Home, home)
	}
	if got := pwd(t, tm.Run); got != resolve(t, home) {
		t.Errorf("tmux commands run in %s, want %s", got, home)
	}
}

func fail(code int, msg string) error { return &ExitError{Command: "tmux", Code: code, Stderr: msg} }

func TestRespawnFailureCarriesTheServerStateAndRetriesOnce(t *testing.T) {
	tm, f := newFake(t)
	var waits []time.Duration
	tm.Sleep = func(d time.Duration) { waits = append(waits, d) }
	f.answer("respawn-pane", "", fail(1, "can't find directory"))
	f.answer("list-panes", "server=42 engine:@0 %0 pid=7 tty=/dev/ttys001 cwd=/gone\n", nil)
	_, err := tm.Open(window(t))
	if err == nil {
		t.Fatal("a respawn that keeps failing was reported as opened")
	}
	for _, want := range []string{"respawn-pane failed", "can't find directory", "server=42", "pid=7", "tty=/dev/ttys001", "cwd=/gone"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	calls := f.dump()
	if n := strings.Count(calls, "respawn-pane"); n != 2 {
		t.Errorf("respawn-pane ran %d times, want 2 (once, then once more)\n%s", n, calls)
	}
	if !strings.Contains(calls, "list-panes -a") || !strings.Contains(calls, "kill-window") {
		t.Errorf("the failed window is removed and the panes are listed:\n%s", calls)
	}
	if len(waits) != 1 || waits[0] != respawnRetryWait {
		t.Errorf("waits %v, want one of %s", waits, respawnRetryWait)
	}
}

func TestRespawnSucceedsOnTheRetry(t *testing.T) {
	tm, f := newFake(t)
	tm.Sleep = func(time.Duration) {}
	tries := 0
	f.answers["respawn-pane"] = func() (string, error) {
		tries++
		if tries == 1 {
			return "", fail(1, "server busy")
		}
		return "", nil
	}
	if _, err := tm.Open(window(t)); err != nil {
		t.Fatal(err)
	}
	if tries != 2 || strings.Contains(f.dump(), "list-panes") {
		t.Errorf("respawn-pane ran %d times:\n%s", tries, f.dump())
	}
}

func TestServerPid(t *testing.T) {
	for _, tc := range []struct {
		name    string
		out     string
		err     error
		pid     int
		running bool
		fails   bool
	}{
		{"a server", "4242\n", nil, 4242, true, false},
		{"no server", "", fail(1, "no server running"), 0, false, false},
		{"no socket", "", fail(1, "error connecting to /tmp/tmux-1/default (No such file or directory)"), 0, false, false},
		{"a socket tmux may not use", "", fail(1, "error connecting to /tmp/tmux-1/default (Permission denied)"), 0, false, true},
		{"tmux cannot run", "", errors.New("tmux: not found"), 0, false, true},
		{"garbage", "none\n", nil, 0, false, true},
	} {
		tm, f := newFake(t)
		f.answer("display-message", tc.out, tc.err)
		pid, running, err := tm.ServerPid()
		if pid != tc.pid || running != tc.running || (err != nil) != tc.fails {
			t.Errorf("%s: pid %d running %v err %v", tc.name, pid, running, err)
		}
	}
}

func TestServerStart(t *testing.T) {
	for _, tc := range []struct {
		name    string
		out     string
		err     error
		start   int64
		running bool
		fails   bool
	}{
		{"a server", "1700000000\n", nil, 1700000000, true, false},
		{"no server", "", fail(1, "no server running"), 0, false, false},
		{"no socket", "", fail(1, "error connecting to /tmp/tmux-1/default (No such file or directory)"), 0, false, false},
		{"a socket tmux may not use", "", fail(1, "error connecting to /tmp/tmux-1/default (Permission denied)"), 0, false, true},
		{"tmux cannot run", "", errors.New("tmux: not found"), 0, false, true},
		{"garbage", "none\n", nil, 0, false, true},
		{"a zero start", "0\n", nil, 0, false, true},
	} {
		tm, f := newFake(t)
		f.answer("display-message", tc.out, tc.err)
		start, running, err := tm.ServerStart()
		if start != tc.start || running != tc.running || (err != nil) != tc.fails {
			t.Errorf("%s: start %d running %v err %v", tc.name, start, running, err)
		}
		if !strings.Contains(f.dump(), "#{start_time}") {
			t.Errorf("%s: asked tmux %q", tc.name, f.dump())
		}
	}
}

func TestLockPathIsPerServer(t *testing.T) {
	tm := &Tmux{Home: "/h"}
	if got := tm.lockPath(); got != "/h/.local/share/engine/tmux-default.lock" {
		t.Errorf("default server: %s", got)
	}
	tm.Socket = "a/b"
	if got := tm.lockPath(); got != "/h/.local/share/engine/tmux-a_b.lock" {
		t.Errorf("private socket: %s", got)
	}
	if got := (&Tmux{}).lockPath(); got != "" {
		t.Errorf("no home takes no lock: %s", got)
	}
}

// tryLock is true when another open file description could take the lock now.
func tryLock(t *testing.T, path string) bool {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil && !errors.Is(err, syscall.EWOULDBLOCK) {
		t.Fatal(err)
	}
	return err == nil
}

// Open holds the host's lock for the whole of its tmux commands, so two
// callers never create windows on one server at once, and frees it after.
func TestOpenHoldsThePerHostLock(t *testing.T) {
	tm, f := newFake(t)
	tm.Home = t.TempDir()
	path := tm.lockPath()
	heldDuring := 0
	inner := f.run
	tm.Run = func(name string, args ...string) (string, error) {
		if !tryLock(t, path) {
			heldDuring++
		}
		return inner(name, args...)
	}
	if _, err := tm.Open(window(t)); err != nil {
		t.Fatal(err)
	}
	if heldDuring != len(f.calls) || len(f.calls) == 0 {
		t.Errorf("the lock was held for %d of %d tmux commands", heldDuring, len(f.calls))
	}
	if !tryLock(t, path) {
		t.Error("Open left the lock held")
	}
}

func TestOpenWithoutAHomeTakesNoLock(t *testing.T) {
	tm, _ := newFake(t)
	if _, err := tm.Open(window(t)); err != nil {
		t.Fatal(err)
	}
}

func TestOpenReportsALockItCannotTake(t *testing.T) {
	tm, f := newFake(t)
	file := filepath.Join(t.TempDir(), "home")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	tm.Home = file // a file: the lock's directory cannot be made
	if _, err := tm.Open(window(t)); err == nil || len(f.calls) != 0 {
		t.Errorf("err %v, tmux calls %v", err, f.calls)
	}
}
