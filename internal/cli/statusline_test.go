package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStatuslineHookCommand(t *testing.T) {
	for _, tc := range []struct {
		input string
		code  int
	}{{`{"session_name":"test","rate_limits":{"five_hour":{"used_percentage":83,"resets_at":2000}}}`, 0}, {`{`, 1}} {
		dir := t.TempDir()
		var out, errs bytes.Buffer
		env := Env{Home: dir, In: strings.NewReader(tc.input), Out: &out, Err: &errs, Now: func() time.Time { return time.Unix(1000, 0) }, Getenv: func(key string) string {
			if key == "ENGINE_RUN_DIR" {
				return dir
			}
			return ""
		}}
		if code := Run([]string{"hook", "statusline"}, env); code != tc.code {
			t.Fatalf("exit %d, %s", code, errs.String())
		}
		if tc.code == 0 {
			if out.String() != "test  5h 83%\n" {
				t.Errorf("%q", out.String())
			}
			if _, err := os.Stat(filepath.Join(dir, "quota.json")); err != nil {
				t.Fatal(err)
			}
		} else if !strings.Contains(errs.String(), "engine hook statusline:") {
			t.Errorf("%s", errs.String())
		}
	}
}
