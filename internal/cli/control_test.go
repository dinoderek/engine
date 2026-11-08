package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/session"
)

func TestServerHookRefusesEveryExecutorMergeAndPointsAtMergeReady(t *testing.T) {
	home := t.TempDir()
	work := t.TempDir()
	dir := filepath.Join(home, session.RunsDir, "demo-1-a2")
	if err := session.WriteRecord(dir, session.Record{Control: true, RunID: "demo-1-a2", Harness: "codex", Mode: "hands-off", Worktree: work}); err != nil {
		t.Fatal(err)
	}
	invoke := func(cwd, command string, getenv func(string) string) (int, string) {
		b, _ := json.Marshal(map[string]any{"cwd": cwd, "tool_name": "Bash", "tool_input": map[string]string{"command": command}})
		var stderr bytes.Buffer
		code := Run([]string{"hook", "pre-tool-use"}, Env{Home: home, In: strings.NewReader(string(b)), Err: &stderr, Out: &bytes.Buffer{}, Getenv: getenv})
		return code, stderr.String()
	}
	none := func(string) string { return "" }
	pinned := "gh pr merge 7 --squash --match-head-commit abc"
	// A session engine started by hand, outside the server, still merges itself.
	if code, msg := invoke(work, pinned, none); code != 0 {
		t.Fatalf("GitHub-mode merge: %d %s", code, msg)
	}
	// A session file that cannot be read fails closed.
	loop := filepath.Join(dir, "server-session.json")
	if err := os.Symlink(loop, loop); err != nil {
		t.Fatal(err)
	}
	if code, _ := invoke(work, pinned, none); code != 2 {
		t.Fatalf("an unreadable session file allowed a merge: %d", code)
	}
	if err := os.Remove(loop); err != nil {
		t.Fatal(err)
	}
	metadata, _ := json.Marshal(struct{ URL, Attempt string }{"http://control.test", "a2"})
	lock, _ := json.Marshal(control.Lock{Token: control.Secret()})
	for name, b := range map[string][]byte{"server-session.json": metadata, "server-token": []byte(control.Secret()), "merge-lock.json": lock} {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Holding the lock is not enough: the coordinator merges.
	if code, msg := invoke(work, pinned, none); code != 2 || !strings.Contains(msg, "engine task merge-ready") {
		t.Fatalf("server merge: %d %s", code, msg)
	}
	if code, msg := invoke(work, "gh pr view 7", none); code != 0 {
		t.Fatalf("a read was blocked: %d %s", code, msg)
	}
	server := func(k string) string { return map[string]string{"ENGINE_SERVER_URL": "http://control.test"}[k] }
	if code, msg := invoke(t.TempDir(), pinned, server); code != 2 || !strings.Contains(msg, "never merges") {
		t.Fatalf("server environment merge: %d %s", code, msg)
	}
}

func TestServerCommandsRouteWithoutLegacyGitHubAccess(t *testing.T) {
	for _, args := range [][]string{{"repo", "list"}, {"plan", "show", "p2"}, {"run", "p2"}, {"start", "t3"}, {"tell", "t3", "hi"}, {"attach", "t3"}, {"ps"}, {"ps", "--server"}, {"lint", "p2"}, {"task", "complete-external", "t3", "--version", "1", "--pr", "url"}} {
		if !serverCommand(args) {
			t.Fatal(args)
		}
	}
	if serverCommand([]string{"run", "o/r" + "#" + "2"}) || serverCommand([]string{"ps", "--github"}) {
		t.Fatal("legacy reference changed source")
	}
}
