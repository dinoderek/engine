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

func TestPluginDir(t *testing.T) {
	none := func(string) string { return "" }
	if got := PluginDir(none, "/home/me"); got != "/home/me/.claude/plugins/cache/engine" {
		t.Errorf("default: %s", got)
	}
	dir := func(k string) string { return map[string]string{"CLAUDE_CONFIG_DIR": "/cfg"}[k] }
	if got := PluginDir(dir, "/home/me"); got != "/cfg/plugins/cache/engine" {
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

func TestTrustFolderFollowsASymlinkedFile(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "dotfiles", "claude.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(root, ".claude.json")
	if err := os.Symlink(target, config); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := TrustFolder(config, dir); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(config); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the symlink was replaced: %v, %v", info.Mode(), err)
	}
	if !trusted(t, target, dir) {
		t.Errorf("the target is not updated:\n%s", readFile(target))
	}
}

// writerFor is a stamp check that sees another writer change the file the
// first n times engine is about to rename it.
func writerFor(n int) func(string) (fileStamp, error) {
	return func(path string) (fileStamp, error) {
		st, err := stamp(path)
		if n > 0 {
			n--
			st.size++
		}
		return st, err
	}
}

func TestTrustFolderRetriesWhenTheFileChanges(t *testing.T) {
	config := filepath.Join(t.TempDir(), ".claude.json")
	dir := t.TempDir()
	if err := os.WriteFile(config, []byte(`{"a": 1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := trustFolder(config, dir, writerFor(trustAttempts-1)); err != nil {
		t.Fatal(err)
	}
	if !trusted(t, config, dir) {
		t.Errorf("the last attempt should succeed:\n%s", readFile(config))
	}
}

func TestTrustFolderGivesUpOnABusyFile(t *testing.T) {
	config := filepath.Join(t.TempDir(), ".claude.json")
	dir := t.TempDir()
	orig := `{"a": 1}`
	if err := os.WriteFile(config, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	err := trustFolder(config, dir, writerFor(trustAttempts))
	if err == nil || !strings.Contains(err.Error(), "kept changing") {
		t.Errorf("err = %v", err)
	}
	if readFile(config) != orig {
		t.Errorf("a busy file was overwritten:\n%s", readFile(config))
	}
	if leftovers, _ := filepath.Glob(config + ".engine-*"); len(leftovers) > 0 {
		t.Errorf("temp files left: %v", leftovers)
	}
}
