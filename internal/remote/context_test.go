package remote

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"
)

func TestContextExecPreservesStreamsExitAndCancellation(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh absent")
	}
	var out, stderr bytes.Buffer
	run := ExecContext(context.Background(), time.Minute)
	code, err := run(nil, &out, &stderr, "sh", "-c", "printf output; printf failure >&2; exit 7")
	if code != 7 || err != nil || out.String() != "output" || stderr.String() != "failure" {
		t.Fatal(code, err, &out, &stderr)
	}
	if code, err = run(nil, &out, &stderr, "sh", "-c", "exit 0"); code != 0 || err != nil {
		t.Fatal(code, err)
	}
	if _, err = run(nil, &out, &stderr, "/missing-engine-tool"); err == nil {
		t.Fatal("missing executable accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = ExecContext(ctx, time.Minute)(nil, &out, &stderr, "sh", "-c", "exit 0"); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored", err)
	}
}
