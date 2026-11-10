package controlcmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/control"
)

// A session reports its cleanup with only the window's environment: the
// server's URL, the credential file and the attempt. Nothing comes from the
// worktree, which may already be gone.
func TestCLICleanedReportsFromTheEnvironmentAlone(t *testing.T) {
	e, svc := environment(t)
	repo := command(t, e, "repo", "add", "--title", "Demo", "--slug", "x/y", "--remote", "remote").Item
	plan := command(t, e, "plan", "create", "--repo", repo.ID, "--title", "Feature").Item
	command(t, e, "plan", "approve", plan.ID, "--version", "1")
	command(t, e, "run", plan.ID)
	task := command(t, e, "task", "create", "--repo", repo.ID, "--plan", plan.ID, "--title", "Build").Item
	at, err := svc.Dispatch(*task, "devbox", "claude", "dispatch")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordMerged(task.ID, fmt.Sprintf("https://github.com/x/y/pull/%d", 7), "merged"); err != nil {
		t.Fatal(err)
	}
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte(at.Credential+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"ENGINE_SERVER_URL": e.Client.URL, "ENGINE_SERVER_TOKEN_FILE": tokenFile, "ENGINE_ATTEMPT_ID": at.Attempt.ID}
	e.Getenv = func(k string) string { return env[k] }
	e.Client = nil
	e.Home = filepath.Join(t.TempDir(), "no-home")

	out := &bytes.Buffer{}
	e.Out = out
	if code := Main([]string{"task", "cleaned", task.ID, "--left", "a volume"}, e); code != 0 {
		t.Fatalf("task cleaned exited %d: %s", code, e.Err)
	}
	var res control.Response
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if u := res.Updates[0]; u.Kind != control.Cleaned || u.Left != "a volume" || u.Item != task.ID {
		t.Fatalf("update %+v", res.Updates)
	}

	for _, args := range [][]string{{"plan", "cleaned", "p1"}, {"tell", "t1", "hi", "--left", "x"}, {"task", "cleaned", "t1", "extra"}} {
		if _, err := Parse(args, e); err == nil {
			t.Fatalf("%v parsed", args)
		}
	}
	if !strings.Contains(Usage, "engine task cleaned <task-id> [--left <text>]") {
		t.Fatal("the usage lacks task cleaned")
	}
}
