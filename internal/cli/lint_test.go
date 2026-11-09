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

func TestLintServerPlan(t *testing.T) {
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
	}}
	if code := Run([]string{"lint", "p2"}, env); code != 0 || !strings.Contains(out.String(), "p2: the plan passes, 1 tasks") {
		t.Fatalf("exit %d, out %s, err %s", code, &out, &errOut)
	}
}
