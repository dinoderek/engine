package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/session"
)

func TestLimitHookCommand(t *testing.T) {
	for _, tc := range []struct {
		name, input  string
		run, missing bool
		code         int
	}{
		{"stop", `{"hook_event_name":"StopFailure","error":"rate_limit","resets_at":2000}`, true, false, 0},
		{"invalid input", "{", true, false, 1},
		{"outside engine", "{", false, false, 0},
		{"missing run", `{"hook_event_name":"StopFailure","error":"billing_error"}`, true, true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.missing {
				dir = filepath.Join(dir, "missing")
			}
			var out, errs bytes.Buffer
			env := Env{Home: t.TempDir(), In: strings.NewReader(tc.input), Out: &out, Err: &errs, Now: func() time.Time { return time.Unix(1000, 0) }, Getenv: func(key string) string {
				if key == session.RunDirVar && tc.run {
					return dir
				}
				return ""
			}}
			if code := Run([]string{"hook", "limit"}, env); code != tc.code {
				t.Fatalf("exit %d: %s", code, errs.String())
			}
			if tc.code != 0 && !strings.Contains(errs.String(), "engine hook limit:") {
				t.Errorf("stderr %s", errs.String())
			}

			if out.Len() != 0 {
				t.Errorf("output %q", out.String())
			}
		})
	}
}

func TestLimitHookWritesMarkerAndTelemetry(t *testing.T) {
	dir := t.TempDir()
	var errs bytes.Buffer
	env := Env{Home: t.TempDir(), In: strings.NewReader(`{"hook_event_name":"StopFailure","error":"rate_limit","resets_at":2000}`), Out: &bytes.Buffer{}, Err: &errs, Now: func() time.Time { return time.Unix(1000, 0) }, Getenv: func(key string) string {
		if key == session.RunDirVar {
			return dir
		}
		return ""
	}}
	if code := Run([]string{"hook", "limit"}, env); code != 0 {
		t.Fatalf("exit %d: %s", code, errs.String())
	}
	if l, ok := session.ReadLimit(dir); !ok || l.ResetsAt != 2000 {
		t.Errorf("marker %+v, %v", l, ok)
	}
	if _, err := os.Stat(filepath.Join(dir, "limits.jsonl")); err != nil {
		t.Fatal(err)
	}
}
