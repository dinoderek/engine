package session

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunCommandWithinReturnsOutputInTime(t *testing.T) {
	run := RunCommandWithin(10 * time.Second)
	out, err := run(t.TempDir(), []string{"ENGINE_TEST=hello"}, "sh", "-c", `printf %s "$ENGINE_TEST"; exit 2`)
	if out != "hello" || err == nil {
		t.Errorf("out %q, err %v: want the output and the exit error", out, err)
	}
}

func TestRunCommandWithinKillsAProcessPastItsDeadline(t *testing.T) {
	run := RunCommandWithin(200 * time.Millisecond)
	began := time.Now()
	// A child that outlives the shell still holds the pipe; the call must not
	// wait for it, and the deadline kills it too.
	pidFile := filepath.Join(t.TempDir(), "child")
	out, err := run("", nil, "sh", "-c", "echo started; sleep 30 & echo $! > "+pidFile+"; sleep 30")
	if took := time.Since(began); took > 10*time.Second {
		t.Fatalf("returned after %s", took)
	}
	if err == nil || !strings.Contains(err.Error(), "no answer within 200ms") {
		t.Errorf("err = %v", err)
	}
	if !strings.Contains(out, "started") {
		t.Errorf("out = %q, want what it printed before the deadline", out)
	}
	raw, readErr := os.ReadFile(pidFile)
	pid, convErr := strconv.Atoi(strings.TrimSpace(string(raw)))
	if readErr != nil || convErr != nil {
		t.Fatalf("child pid: %v, %v", readErr, convErr)
	}
	// Signal 0 only checks; give the kernel a moment to reap the killed group.
	for i := 0; i < 50 && syscall.Kill(pid, 0) == nil; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if syscall.Kill(pid, 0) == nil {
		t.Errorf("the background child %d outlived the deadline", pid)
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}
