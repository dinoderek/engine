package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// lintGh serves a feature with one task whose body is body.
func lintGh(body string) func([]string, string, ...string) (string, error) {
	task, _ := json.Marshal(map[string]any{"number": 2, "title": "t", "state": "open", "body": body,
		"repository_url": "https://api.github.com/repos/o/r", "labels": []any{}})
	return func(_ []string, _ string, args ...string) (string, error) {
		switch {
		case strings.HasSuffix(args[1], "/sub_issues"):
			return "[" + string(task) + "]", nil
		case strings.HasSuffix(args[1], "/blocked_by"):
			return "[]", nil
		}
		return `{"number":1,"title":"F","state":"open","labels":[]}`, nil
	}
}

func TestLint(t *testing.T) {
	good := "## Outcome\nx\n## Tests\ny\n## Requires\nnone\n<!-- engine:card k -->"
	tests := []struct {
		name, body, stdout string
		code               int
	}{
		{"passes", good, "the plan passes, 1 tasks", 0},
		{"fails", "## Outcome\nx", "3 problem(s)", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			env := Env{Out: &out, Err: &errOut, Exec: lintGh(tt.body), Getenv: func(k string) string { return map[string]string{"GH_TOKEN": "tok"}[k] }}
			if code := Run([]string{"lint", feature}, env); code != tt.code || !strings.Contains(out.String(), tt.stdout) {
				t.Errorf("exit %d, stdout %q, stderr %q", code, out.String(), errOut.String())
			}
		})
	}
}
