package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/session"
)

func TestServerHookFindsJoinedSessionAndFailsClosedWithoutLock(t *testing.T) {
	fail := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req control.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if req.Op != "lock-check" {
			t.Errorf("unexpected op %s", req.Op)
		}
		if fail {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":"wrong lock"}`))
			return
		}
		_, _ = w.Write([]byte("{}"))
	}))
	defer server.Close()
	home := t.TempDir()
	work := t.TempDir()
	dir := filepath.Join(home, session.RunsDir, "demo-1-a2")
	if err := session.WriteRecord(dir, session.Record{Control: true, RunID: "demo-1-a2", Harness: "codex", Mode: "hands-off", Worktree: work}); err != nil {
		t.Fatal(err)
	}
	metadata, _ := json.Marshal(struct{ URL, Attempt string }{server.URL, "a2"})
	for name, b := range map[string][]byte{"server-session.json": metadata, "server-token": []byte(control.Secret())} {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	raw := func() string {
		b, _ := json.Marshal(map[string]any{"cwd": work, "tool_name": "Bash", "tool_input": map[string]string{"command": "gh pr merge --squash --match-head-commit abc"}})
		return string(b)
	}
	var stderr bytes.Buffer
	env := Env{Home: home, Err: &stderr, Out: &bytes.Buffer{}, Getenv: func(string) string { return "" }}
	invoke := func() int { env.In = strings.NewReader(raw()); return Run([]string{"hook", "pre-tool-use"}, env) }
	if got := invoke(); got != 2 {
		t.Fatalf("missing lock: %d %s", got, &stderr)
	}
	lock, _ := json.Marshal(control.Lock{Token: control.Secret()})
	if err := os.WriteFile(filepath.Join(dir, "merge-lock.json"), lock, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := invoke(); got != 0 {
		t.Fatalf("valid lock: %d %s", got, &stderr)
	}
	fail = true
	if got := invoke(); got != 2 {
		t.Fatal("rejected ownership did not block merge")
	}
}

func TestServerCommandsRouteWithoutLegacyGitHubAccess(t *testing.T) {
	for _, args := range [][]string{{"repo", "list"}, {"plan", "show", "p2"}, {"run", "p2"}, {"start", "t3"}, {"tell", "t3", "hi"}, {"attach", "t3"}, {"ps", "--server"}, {"lint", "p2"}, {"task", "complete-external", "t3", "--version", "1", "--pr", "url"}} {
		if !serverCommand(args, Env{}) {
			t.Fatal(args)
		}
	}
	if serverCommand([]string{"run", "o/r" + "#" + "2"}, Env{}) {
		t.Fatal("legacy reference changed source")
	}
}
