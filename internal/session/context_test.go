package session

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"
)

func TestContextRunnersPreserveExitAndCancellation(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh absent")
	}
	runner := ExecContext(context.Background(), time.Minute)
	out, err := runner("sh", "-c", "printf output; printf failure >&2; exit 7")
	var exit *ExitError
	if out != "output" || !errors.As(err, &exit) || exit.Code != 7 || exit.Stderr != "failure" {
		t.Fatal(out, err)
	}
	if _, err = runner("sh", "-c", "exit 0"); err != nil {
		t.Fatal(err)
	}
	if _, err = runner("/missing-engine-tool"); err == nil {
		t.Fatal("missing executable accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = ExecContext(ctx, time.Minute)("sh", "-c", "exit 0"); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored", err)
	}
	if _, err = RunCommandContext(ctx, time.Minute)("", nil, "sh", "-c", "exit 0"); err == nil {
		t.Fatal("run cancellation ignored")
	}
}
