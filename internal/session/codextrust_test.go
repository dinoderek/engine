package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// codexConfig is an engine Codex home's config.toml as setup and Codex leave
// it: setup's block, then Codex's tables.
const codexConfig = `# engine setup writes this block; Codex's own settings follow it.
sandbox_mode = "workspace-write"

[sandbox_workspace_write]
network_access = true

[plugins."engine@engine"]
enabled = true
`

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func trustTable(dir string) string {
	return "[projects." + TOMLString(dir) + "]\ntrust_level = \"trusted\"\n"
}

// Trust appends a table for the folder and keeps every other line; untrust
// takes it out again, leaving the file as it was.
func TestTrustCodexFolder(t *testing.T) {
	config := writeConfig(t, codexConfig)
	if err := TrustCodexFolder(config, "/w/task-r-7"); err != nil {
		t.Fatal(err)
	}
	want := codexConfig + "\n" + trustTable("/w/task-r-7")
	if got := readFile(config); got != want {
		t.Errorf("trusted:\n%s\nwant:\n%s", got, want)
	}
	if info, err := os.Stat(config); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, %v", info.Mode(), err)
	}
	// Trusting again changes nothing.
	if err := TrustCodexFolder(config, "/w/task-r-7"); err != nil || readFile(config) != want {
		t.Errorf("trusted twice: %v\n%s", err, readFile(config))
	}
	if err := UntrustCodexFolder(config, "/w/task-r-7"); err != nil {
		t.Fatal(err)
	}
	if got := readFile(config); got != codexConfig {
		t.Errorf("untrusted:\n%s\nwant:\n%s", got, codexConfig)
	}
}

// Untrusting one folder leaves the others, and Codex's tables after it.
func TestUntrustCodexFolderLeavesTheRest(t *testing.T) {
	config := writeConfig(t, codexConfig)
	for _, dir := range []string{"/w/a", "/w/b"} {
		if err := TrustCodexFolder(config, dir); err != nil {
			t.Fatal(err)
		}
	}
	codexLater := "[projects.\"/elsewhere\"]\ntrust_level = \"untrusted\"\n"
	if err := os.WriteFile(config, []byte(readFile(config)+"\n"+codexLater), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UntrustCodexFolder(config, "/w/a"); err != nil {
		t.Fatal(err)
	}
	want := codexConfig + "\n" + trustTable("/w/b") + "\n" + codexLater
	if got := readFile(config); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// A table Codex wrote for the folder, with either quoting, is replaced, not
// repeated.
func TestTrustCodexFolderReplacesAnEarlierTable(t *testing.T) {
	for _, header := range []string{`[projects."/w/a"]`, `[projects.'/w/a']`} {
		config := writeConfig(t, header+"\ntrust_level = \"untrusted\"\n\n[x]\ny = 1\n")
		if err := TrustCodexFolder(config, "/w/a"); err != nil {
			t.Fatal(err)
		}
		if got, want := readFile(config), "[x]\ny = 1\n\n"+trustTable("/w/a"); got != want {
			t.Errorf("%s: got:\n%s\nwant:\n%s", header, got, want)
		}
	}
}

// A folder reached through a link is trusted by both paths, and untrusted
// by both.
func TestTrustCodexFolderAddsTheResolvedPath(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(link)
	config := writeConfig(t, "")
	if err := TrustCodexFolder(config, link); err != nil {
		t.Fatal(err)
	}
	if got, want := readFile(config), trustTable(link)+"\n"+trustTable(resolved); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if err := UntrustCodexFolder(config, link); err != nil || strings.TrimSpace(readFile(config)) != "" {
		t.Errorf("untrusted: %v\n%q", err, readFile(config))
	}
}

// A missing file is created by trust, and left missing by untrust.
func TestCodexFolderTrustMissingFile(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config.toml")
	if err := UntrustCodexFolder(config, "/w/a"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(config); !os.IsNotExist(err) {
		t.Errorf("untrust made the file: %v", err)
	}
	if err := TrustCodexFolder(config, "/w/a"); err != nil || readFile(config) != trustTable("/w/a") {
		t.Errorf("trust: %v\n%s", err, readFile(config))
	}
	if err := TrustCodexFolder(filepath.Join(dir, "none", "config.toml"), "/w/a"); err == nil {
		t.Error("trust in a missing home succeeded")
	}
}

// A symlinked config.toml is edited at its target.
func TestTrustCodexFolderFollowsASymlinkedFile(t *testing.T) {
	target := writeConfig(t, codexConfig)
	config := filepath.Join(t.TempDir(), "config.toml")
	if err := os.Symlink(target, config); err != nil {
		t.Fatal(err)
	}
	if err := TrustCodexFolder(config, "/w/a"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(config); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the symlink was replaced: %v", err)
	}
	if !strings.Contains(readFile(target), trustTable("/w/a")) {
		t.Errorf("the target is not updated:\n%s", readFile(target))
	}
}

func TestCodexFolderTrustRetriesWhenTheFileChanges(t *testing.T) {
	config := writeConfig(t, codexConfig)
	add := func(text string, keys []string) string { return text + "# " + keys[0] + "\n" }
	if err := editCodexConfig(config, "/w/a", writerFor(trustAttempts-1), add); err != nil {
		t.Fatal(err)
	}
	if got := readFile(config); got != codexConfig+"# /w/a\n" {
		t.Errorf("the last attempt should succeed:\n%s", got)
	}
	err := editCodexConfig(config, "/w/b", writerFor(trustAttempts), add)
	if err == nil || !strings.Contains(err.Error(), "kept changing") {
		t.Errorf("err = %v", err)
	}
	if strings.Contains(readFile(config), "/w/b") {
		t.Errorf("a busy file was overwritten:\n%s", readFile(config))
	}
}
