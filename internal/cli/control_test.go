package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/session"
)

func TestHookRefusesEveryMergeInAnEngineSessionAndPointsAtMergeReady(t *testing.T) {
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
	// Outside an engine session, the ordinary guard holds: a pinned merge
	// passes, an unpinned one does not.
	if code, msg := invoke(t.TempDir(), pinned, none); code != 0 {
		t.Fatalf("a pinned merge outside a session: %d %s", code, msg)
	}
	if code, msg := invoke(t.TempDir(), "gh pr merge 7 --squash", none); code != 2 || !strings.Contains(msg, "--match-head-commit") {
		t.Fatalf("an unpinned merge outside a session: %d %s", code, msg)
	}
	// Inside one, found by the run record or by the environment, every merge
	// is refused, pinned or not, and the refusal names merge-ready.
	inRun := func(k string) string { return map[string]string{session.RunDirVar: dir}[k] }
	server := func(k string) string { return map[string]string{"ENGINE_SERVER_URL": "http://control.test"}[k] }
	for name, c := range map[string]struct {
		cwd    string
		getenv func(string) string
	}{"run record": {work, none}, "run directory variable": {t.TempDir(), inRun}, "server URL": {t.TempDir(), server}} {
		for _, command := range []string{pinned, "gh pr merge 7 --rebase --match-head-commit abc"} {
			if code, msg := invoke(c.cwd, command, c.getenv); code != 2 || !strings.Contains(msg, "engine task merge-ready") || !strings.Contains(msg, "never merges") {
				t.Fatalf("%s, %q: %d %s", name, command, code, msg)
			}
		}
		if code, msg := invoke(c.cwd, "gh pr view 7", c.getenv); code != 0 {
			t.Fatalf("%s: a read was blocked: %d %s", name, code, msg)
		}
	}
}

func TestServerCommandsRouteByTheirIDs(t *testing.T) {
	for _, args := range [][]string{{"repo", "list"}, {"plan", "show", "p2"}, {"run", "p2"}, {"run", "t3"}, {"tell", "t3", "hi"}, {"attach", "t3"}, {"ps"}, {"ps", "--json"}, {"lint", "p2"}, {"task", "complete-external", "t3", "--version", "1", "--pr", "url"}} {
		if !serverCommand(args) {
			t.Fatal(args)
		}
	}
	for _, args := range [][]string{{"run", ghIssue}, {"run"}, {"ps", "--github"}, {"tell", "r-7", "hi"}, {"attach", "r-7"}, {"lint", ghIssue}, {"start", "t3"}, {"labels", "sync", "o/r"}} {
		if serverCommand(args) {
			t.Fatal("not a server form:", args)
		}
	}
}
