package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const request = `{"hook_event_name":"PermissionRequest","tool_name":"Read","tool_input":{"file_path":"/opt/x"}}`

func TestPermissionHook(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name   string
		vars   map[string]string
		denied bool
		logged bool
		stderr string
	}{
		{"unattended, with a run directory", map[string]string{"ENGINE_UNATTENDED": "1", "ENGINE_RUN_DIR": dir}, true, true, ""},
		{"unattended, no run directory", map[string]string{"ENGINE_UNATTENDED": "1"}, true, false, ""},
		{"unattended, missing run directory", map[string]string{"ENGINE_UNATTENDED": "1", "ENGINE_RUN_DIR": filepath.Join(dir, "gone")}, true, false, "open the permission log"},
		{"attended", map[string]string{"ENGINE_RUN_DIR": dir}, false, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = os.Remove(filepath.Join(dir, "permissions.jsonl"))
			var out, errOut bytes.Buffer
			env := Env{In: strings.NewReader(request), Out: &out, Err: &errOut, Getenv: func(k string) string { return tt.vars[k] },
				Now: func() time.Time { return time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC) }}
			if code := Run([]string{"hook", "permission"}, env); code != 0 {
				t.Fatalf("exit %d, stderr %s", code, errOut.String())
			}
			if got := strings.Contains(out.String(), `"deny"`); got != tt.denied {
				t.Errorf("denied %v, want %v: %q", got, tt.denied, out.String())
			}
			log, _ := os.ReadFile(filepath.Join(dir, "permissions.jsonl"))
			if got := strings.Contains(string(log), `"tool":"Read"`); got != tt.logged {
				t.Errorf("logged %v, want %v: %q", got, tt.logged, log)
			}
			if !strings.Contains(errOut.String(), tt.stderr) {
				t.Errorf("stderr %q lacks %q", errOut.String(), tt.stderr)
			}
		})
	}
}
