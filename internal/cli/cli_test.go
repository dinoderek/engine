package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		code   int
		stdout string
		stderr string
	}{
		{"no command", nil, 2, "", "usage: engine"},
		{"version", []string{"version"}, 0, "engine ", ""},
		{"help", []string{"--help"}, 0, "usage: engine", ""},
		{"unknown", []string{"frobnicate"}, 2, "", `unknown command "frobnicate"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			if code := Run(tt.args, &out, &errOut); code != tt.code {
				t.Errorf("exit code %d, want %d", code, tt.code)
			}
			if !strings.Contains(out.String(), tt.stdout) {
				t.Errorf("stdout %q lacks %q", out.String(), tt.stdout)
			}
			if !strings.Contains(errOut.String(), tt.stderr) {
				t.Errorf("stderr %q lacks %q", errOut.String(), tt.stderr)
			}
		})
	}
}
