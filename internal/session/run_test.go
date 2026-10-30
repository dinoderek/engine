package session

import (
	"strings"
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
	// A child that outlives the shell still holds the pipe; the call must not wait for it.
	out, err := run("", nil, "sh", "-c", "echo started; sleep 30 & sleep 30")
	if took := time.Since(began); took > 10*time.Second {
		t.Fatalf("returned after %s", took)
	}
	if err == nil || !strings.Contains(err.Error(), "no answer within 200ms") {
		t.Errorf("err = %v", err)
	}
	if !strings.Contains(out, "started") {
		t.Errorf("out = %q, want what it printed before the deadline", out)
	}
}
