package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A scratch worktree checks out origin's default head, detached, replaces a
// leftover, and leaves no worktree record once removed.
func TestScratchWorktreeOfTheDefaultHead(t *testing.T) {
	remote := newRemote(t, "main")
	remote.commit(t, "main", "a.txt", "a", 0o644)
	second := remote.commit(t, "main", "dev", "#!/bin/sh\n", 0o755)
	r := remote.repos(t)
	repo := mustRepo(t, "o/r")
	clone, err := r.Sync(repo)
	if err != nil {
		t.Fatal(err)
	}
	head, err := r.Head(clone)
	if err != nil || head != second {
		t.Fatalf("head %q, %v; want %s", head, err, second)
	}
	if url, err := r.Origin(repo); err != nil || url != "file://"+remote.base+"/o/r.git" {
		t.Errorf("origin %q, %v", url, err)
	}
	dir := filepath.Join(t.TempDir(), "probe", "worktree")
	for range 2 { // the second replaces the first's leftover
		if err := r.AddScratch(clone, dir, head); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "junk"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := mustGit(t, dir, "rev-parse", "HEAD"); got != head {
		t.Errorf("scratch at %s, want %s", got, head)
	}
	if branch := mustGit(t, dir, "branch", "--show-current"); branch != "" {
		t.Errorf("scratch is on branch %q, want detached", branch)
	}
	if err := r.RemoveScratch(clone, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("scratch left: %v", err)
	}
	if list := mustGit(t, clone.Path, "worktree", "list"); strings.Count(list, "\n") != 0 {
		t.Errorf("worktree records left:\n%s", list)
	}
	if err := r.RemoveScratch(clone, dir); err != nil {
		t.Errorf("removing it again: %v", err)
	}
}

func TestOriginNeedsAClone(t *testing.T) {
	r := Repos{Run: realGit, Home: t.TempDir()}
	if _, err := r.Origin(mustRepo(t, "o/r")); err == nil || !strings.Contains(err.Error(), "no clone of o/r") {
		t.Errorf("got %v", err)
	}
}
