package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexHome(t *testing.T) {
	if got := CodexHome(func(string) string { return "" }, "/home/me"); got != "/home/me/.codex" {
		t.Errorf("default: %s", got)
	}
	if got := CodexHome(func(k string) string { return map[string]string{"CODEX_HOME": "/cx"}[k] }, "/home/me"); got != "/cx" {
		t.Errorf("CODEX_HOME: %s", got)
	}
}

// The profile trusts the folder and its resolved path, makes the clone a
// writable root, is readable by its owner only, and goes again with nothing
// left.
func TestCodexProfile(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "codex")
	if err := WriteCodexProfile(home, CodexProfile("r-7"), link, "/c/o/r"); err != nil {
		t.Fatal(err)
	}
	path := CodexProfilePath(home, "engine-r-7")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(link)
	for _, dir := range []string{link, resolved} {
		if !strings.Contains(string(data), "[projects."+TOMLString(dir)+"]\ntrust_level = \"trusted\"\n") {
			t.Errorf("profile does not trust %s:\n%s", dir, data)
		}
	}
	if !strings.HasSuffix(string(data), "\n[sandbox_workspace_write]\nwritable_roots = [\"/c/o/r\"]\n") {
		t.Errorf("profile does not make the clone writable:\n%s", data)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, %v", info.Mode(), err)
	}
	if removed, err := RemoveCodexProfile(home, "engine-r-7"); !removed || err != nil {
		t.Errorf("remove: %v, %v", removed, err)
	}
	if removed, err := RemoveCodexProfile(home, "engine-r-7"); removed || err != nil {
		t.Errorf("remove again: %v, %v", removed, err)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Errorf("left %v", entries)
	}
}

func TestCodexProfileRefusesABadName(t *testing.T) {
	if err := WriteCodexProfile(t.TempDir(), "../x", "/w", "/c"); err == nil || !strings.Contains(err.Error(), `codex profile "../x"`) {
		t.Errorf("got %v", err)
	}
}

func TestTomlString(t *testing.T) {
	for in, want := range map[string]string{
		"/a/b":      `"/a/b"`,
		`a"b\c`:     `"a\"b\\c"`,
		"tab\there": `"tab\u0009here"`,
		"día/ü":     `"día/ü"`,
	} {
		if got := TOMLString(in); got != want {
			t.Errorf("TOMLString(%q) = %s, want %s", in, got, want)
		}
	}
}
