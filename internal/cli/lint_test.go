package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/dinoderek/engine/internal/control"
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

func TestLintServerPlanRoutesWithoutGitHub(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req control.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if req.Op != "plan-view" || req.ID != "p2" {
			t.Errorf("unexpected request %+v", req)
		}
		_ = json.NewEncoder(w).Encode(control.Response{Items: []control.Item{
			{ID: "r1", Kind: "repo", Slug: "owner/demo"},
			{ID: "p2", Kind: "plan", Repo: "r1", State: "prepare"},
			{ID: "t3", Kind: "task", Repo: "r1", Plan: "p2", State: "todo", Body: "## Outcome\nWorks\n## Tests\nProve it"},
		}})
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(control.Secret()), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	env := Env{Out: &out, Err: &errOut, Home: t.TempDir(), Getenv: func(k string) string {
		return map[string]string{"ENGINE_SERVER_URL": server.URL, "ENGINE_SERVER_TOKEN_FILE": path}[k]
	}, Exec: func([]string, string, ...string) (string, error) {
		t.Fatal("server lint called GitHub")
		return "", nil
	}}
	if code := Run([]string{"lint", "p2"}, env); code != 0 || !strings.Contains(out.String(), "p2: the plan passes, 1 tasks") {
		t.Fatalf("exit %d, out %s, err %s", code, &out, &errOut)
	}
}
