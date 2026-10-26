package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFindRun(t *testing.T) {
	home := t.TempDir()
	runs := filepath.Join(home, filepath.FromSlash(RunsDir))
	tree := filepath.Join(home, "trees")
	at := time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)
	add := func(id, worktree string, spawned time.Time) {
		t.Helper()
		if err := WriteRecord(filepath.Join(runs, id), Record{Schema: RecordSchema, RunID: id, Worktree: worktree, Harness: "codex", Spawned: spawned}); err != nil {
			t.Fatal(err)
		}
	}
	a, b, nested := filepath.Join(tree, "a"), filepath.Join(tree, "b"), filepath.Join(tree, "a", "inner")
	for _, d := range []string{filepath.Join(a, "sub", "deeper"), nested, b} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	add("a-old", a, at)
	add("a-new", a, at.Add(time.Hour))
	add("a-older", a, at.Add(-time.Hour))
	add("inner", nested, at.Add(-2*time.Hour))
	add("b", b, at)
	add("no-worktree", "", at.Add(5*time.Hour))
	if err := os.MkdirAll(filepath.Join(runs, "check-1"), 0o755); err != nil { // a check's directory has no run.json
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runs, "stray"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(runs, "broken"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runs, "broken", RecordFile), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, cwd, want string
	}{
		{"the worktree", a, "a-new"},
		{"a folder below it", filepath.Join(a, "sub", "deeper"), "a-new"},
		{"the deepest worktree", nested, "inner"},
		{"a sibling whose name starts the same", a + "-other", ""},
		{"another worktree", b, "b"},
		{"elsewhere", home, ""},
		{"no cwd", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, rec, ok := FindRun(home, tt.cwd)
			if tt.want == "" {
				if ok {
					t.Errorf("found %s", dir)
				}
				return
			}
			if !ok || filepath.Base(dir) != tt.want || rec.RunID != tt.want {
				t.Errorf("found %s (%v), want %s", dir, ok, tt.want)
			}
		})
	}
	if _, _, ok := FindRun("", a); ok {
		t.Error("found a run with no home")
	}
	if _, _, ok := FindRun(t.TempDir(), a); ok {
		t.Error("found a run in a home with no runs directory")
	}
}

func TestFindRunFollowsSymlinks(t *testing.T) {
	home := t.TempDir()
	real := filepath.Join(home, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("no symlinks:", err)
	}
	if err := WriteRecord(filepath.Join(home, filepath.FromSlash(RunsDir), "r"), Record{Worktree: real}); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := FindRun(home, link); !ok {
		t.Error("a cwd reached through a link finds nothing")
	}
}
