package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClaudeConfig(t *testing.T) {
	none := func(string) string { return "" }
	if got := ClaudeConfig(none, "/home/me"); got != "/home/me/.claude.json" {
		t.Errorf("default: %s", got)
	}
	dir := func(k string) string { return map[string]string{"CLAUDE_CONFIG_DIR": "/cfg"}[k] }
	if got := ClaudeConfig(dir, "/home/me"); got != "/cfg/.claude.json" {
		t.Errorf("CLAUDE_CONFIG_DIR: %s", got)
	}
}

func trusted(t *testing.T, config, dir string) bool {
	t.Helper()
	var c struct {
		Projects map[string]struct {
			HasTrustDialogAccepted bool `json:"hasTrustDialogAccepted"`
		} `json:"projects"`
	}
	if err := json.Unmarshal([]byte(readFile(config)), &c); err != nil {
		t.Fatalf("%s: %v", config, err)
	}
	return c.Projects[dir].HasTrustDialogAccepted
}

func TestTrustFolderCreatesTheFile(t *testing.T) {
	config := filepath.Join(t.TempDir(), ".claude.json")
	dir := t.TempDir()
	if err := TrustFolder(config, dir); err != nil {
		t.Fatal(err)
	}
	if !trusted(t, config, dir) {
		t.Errorf("not trusted:\n%s", readFile(config))
	}
	if info, err := os.Stat(config); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("a new file is private: %v, %v", info.Mode(), err)
	}
}

func TestTrustFolderKeepsEverythingElse(t *testing.T) {
	config := filepath.Join(t.TempDir(), ".claude.json")
	dir := t.TempDir()
	orig := `{"numStartups": 12345678901234567890, "tip": "a <b> & c",
 "projects": {"/other": {"allowedTools": ["Bash"], "hasTrustDialogAccepted": true},
  "` + dir + `": {"allowedTools": [], "hasTrustDialogAccepted": false, "lastCost": 1.50}}}`
	if err := os.WriteFile(config, []byte(orig), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := TrustFolder(config, dir); err != nil {
		t.Fatal(err)
	}
	got := readFile(config)
	if !trusted(t, config, dir) || !trusted(t, config, "/other") {
		t.Errorf("trust:\n%s", got)
	}
	for _, want := range []string{"12345678901234567890", `"a <b> & c"`, `"lastCost": 1.50`, `"Bash"`} {
		if !strings.Contains(got, want) {
			t.Errorf("lost %s:\n%s", want, got)
		}
	}
	if info, _ := os.Stat(config); info.Mode().Perm() != 0o640 {
		t.Errorf("mode %v, want 0640", info.Mode())
	}
	if leftovers, _ := filepath.Glob(config + ".engine-*"); len(leftovers) > 0 {
		t.Errorf("temp files left: %v", leftovers)
	}
}

func TestTrustFolderLeavesATrustedFolderAlone(t *testing.T) {
	config := filepath.Join(t.TempDir(), ".claude.json")
	// Resolved, so the folder has one path only (a macOS temp dir is a symlink).
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	orig := `{"projects":{"` + dir + `":{"hasTrustDialogAccepted":true}}}`
	if err := os.WriteFile(config, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Unix(1, 0)
	if err := os.Chtimes(config, old, old); err != nil {
		t.Fatal(err)
	}
	if err := TrustFolder(config, dir); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(config); readFile(config) != orig || !info.ModTime().Equal(old) {
		t.Errorf("rewrote a file that needed no change:\n%s", readFile(config))
	}
}

func TestTrustFolderAddsTheResolvedPath(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "real")
	link := filepath.Join(root, "link")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(root, ".claude.json")
	if err := TrustFolder(config, link); err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(link)
	if !trusted(t, config, link) || !trusted(t, config, resolved) {
		t.Errorf("want both paths trusted:\n%s", readFile(config))
	}
}

func TestTrustFolderRefusesAStrangeFile(t *testing.T) {
	for name, content := range map[string]string{
		"not JSON":              "nope",
		"an array":              "[]",
		"null":                  "null",
		"projects not a map":    `{"projects": []}`,
		"project not an object": `{"projects": {"@DIR@": 3}}`,
	} {
		config := filepath.Join(t.TempDir(), ".claude.json")
		dir := t.TempDir()
		content = strings.ReplaceAll(content, "@DIR@", dir)
		if err := os.WriteFile(config, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := TrustFolder(config, dir); err == nil || !strings.Contains(err.Error(), "trust the folder by hand") {
			t.Errorf("%s: %v", name, err)
		}
		if readFile(config) != content {
			t.Errorf("%s: the file changed", name)
		}
	}
}

func TestTrustFolderUnreadable(t *testing.T) {
	config := filepath.Join(t.TempDir(), "dir")
	if err := os.Mkdir(config, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := TrustFolder(config, t.TempDir()); err == nil {
		t.Error("a directory is not a config file")
	}
}

func TestRecord(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("x", 3600))
	repo := Repo{Owner: "o", Name: "boga3"}
	if got := RunID(repo, 412, at); got != "boga3-412-20260102T020405Z" {
		t.Errorf("run id %s", got)
	}
	dir, err := RunDir("/home/me", "boga3-412-20260102T020405Z")
	if err != nil || dir != "/home/me/.local/share/engine/runs/boga3-412-20260102T020405Z" {
		t.Errorf("run dir %s, %v", dir, err)
	}
	for _, bad := range []string{"..", "a/b", ""} {
		if _, err := RunDir("/home/me", bad); err == nil {
			t.Errorf("run id %q accepted", bad)
		}
	}
	if _, err := RunDir("", "x"); err == nil {
		t.Error("no home accepted")
	}
	runDir := filepath.Join(t.TempDir(), "runs", "r")
	rec := Record{Schema: RecordSchema, RunID: "r", Task: "t", Spawned: at.UTC()}
	if err := WriteRecord(runDir, rec); err != nil {
		t.Fatal(err)
	}
	var back Record
	if err := json.Unmarshal([]byte(readFile(filepath.Join(runDir, RecordFile))), &back); err != nil || back != rec {
		t.Errorf("read back %+v, %v", back, err)
	}
	if text := readFile(filepath.Join(runDir, RecordFile)); !strings.Contains(text, `"spawned": "2026-01-02T02:04:05Z"`) ||
		strings.Contains(text, "feature") {
		t.Errorf("run.json:\n%s", text)
	}
}
