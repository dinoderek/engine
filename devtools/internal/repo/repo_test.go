package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRoot(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "go.mod"), "module x\n")
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Root(sub)
	if err != nil || got != dir {
		t.Fatalf("Root = %q, %v; want %q", got, err, dir)
	}
	if _, err := Root(t.TempDir()); err == nil {
		t.Error("Root outside a module: want an error")
	}
}

func TestFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	mustRun(t, dir, "git", "init", "-q")
	write(t, filepath.Join(dir, ".gitignore"), "ignored.txt\n")
	write(t, filepath.Join(dir, "kept.go"), "package x\n")
	write(t, filepath.Join(dir, "gone.go"), "package x\n")
	write(t, filepath.Join(dir, "ignored.txt"), "x\n")
	mustRun(t, dir, "git", "add", ".")
	if err := os.Remove(filepath.Join(dir, "gone.go")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "new.md"), "x\n")

	got, err := Files(Exec, dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".gitignore", "kept.go", "new.md"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Files = %v, want %v", got, want)
	}
}

func TestExecReportsTheCommand(t *testing.T) {
	_, err := Exec(t.TempDir(), "go", "no-such-subcommand")
	if err == nil || !strings.Contains(err.Error(), "go no-such-subcommand") {
		t.Errorf("err = %v, want it to name the command", err)
	}
}

func TestRows(t *testing.T) {
	got := Rows("# comment\n\na\tb\r\n  # indented comment\nc\n")
	want := []Row{{Line: 3, Fields: []string{"a", "b"}}, {Line: 5, Fields: []string{"c"}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Rows = %v, want %v", got, want)
	}
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustRun(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	if out, err := Exec(dir, name, args...); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}
