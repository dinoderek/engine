package hostops

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/session"
)

func TestControlSpawnUsesOrdinaryCredentialsAndReplays(t *testing.T) {
	h := newHome(t, "mac")
	clone := filepath.Join(h.dir, session.ControlReposDir, "o", "r")
	if err := os.MkdirAll(filepath.Join(clone, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(h.dir, ".config/engine/github-token")); err != nil {
		t.Fatal(err)
	}
	git := &gitFake{}
	tm := &tmuxFake{}
	d := spawnDeps(t, h, git.run, tm, nil)
	r := ControlRequest{Spawn: request(), URL: "http://127.0.0.1:7331", Credential: strings.Repeat("a", 64), Attempt: "a9", Remote: "https://github.com/o/r.git"}
	r.Spawn.Feature = "p3"
	first, err := ControlSpawn(d, r)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ControlSpawn(d, r)
	if err != nil {
		t.Fatal(err)
	}
	if first.Window != second.Window || first.Worktree != second.Worktree {
		t.Fatal("replay lost result")
	}
	if strings.Contains(tm.command, r.Credential) || !strings.Contains(tm.command, "unset GH_TOKEN GITHUB_TOKEN") {
		t.Fatal("launch leaked a token or required the machine user")
	}
	for _, want := range []string{"ENGINE_SERVER_URL", "ENGINE_SERVER_TOKEN_FILE", "ENGINE_ATTEMPT_ID"} {
		if !strings.Contains(tm.command, want) {
			t.Fatal(want)
		}
	}
	for _, line := range git.calls {
		if strings.HasPrefix(line, "gh ") || strings.Contains(line, "credential.helper") || strings.Contains(line, "user.name") {
			t.Fatal("configured machine identity:", line)
		}
	}
	info, err := os.Stat(filepath.Join(first.RunDir, "server-token"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("execution token not private")
	}
	rec, err := session.ReadRecord(first.RunDir)
	if err != nil || !rec.Control {
		t.Fatal(rec, err)
	}
	collected, err := Collect(d, r.Spawn.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(collected.Files, "server-token") {
		t.Fatal("credential included in artifacts")
	}
	r.Credential = "other"
	if _, err = ControlSpawn(d, r); err == nil {
		t.Fatal("another credential replayed the run")
	}
}

func TestControlDispatchJSONAndJoinedEnvironment(t *testing.T) {
	if _, err := readControl(strings.NewReader("invalid")); err == nil {
		t.Fatal("invalid JSON")
	}
	if _, err := ControlSpawn(Deps{}, ControlRequest{}); err == nil {
		t.Fatal("incomplete dispatch")
	}
	l := launch{runDir: "/tmp/run", mode: HandsOff, control: &ControlRequest{URL: "http://server", Attempt: "a1"}}
	vars := sessionVars(l)
	if !strings.Contains(vars, "ENGINE_SERVER_TOKEN_FILE") || !strings.Contains(vars, "ENGINE_ATTEMPT_ID") {
		t.Fatal(vars)
	}
	if !strings.Contains(vars, `GH_TOKEN=""`) || !strings.Contains(vars, `GITHUB_TOKEN=""`) {
		t.Fatal("joined session inherited daemon's machine credentials", vars)
	}
	r := ControlRequest{URL: "http://server", Attempt: "a1", Credential: "secret"}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := readControl(strings.NewReader(string(b)))
	if err != nil || parsed.Credential != "secret" {
		t.Fatal(parsed, err)
	}
	dir := t.TempDir()
	if err = writeControl(dir, r); err != nil {
		t.Fatal(err)
	}
}
