package dev

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/dinoderek/engine/devtools/internal/repo"
)

// clone makes a bare origin with one commit on main and a main checkout
// cloned from it, and returns a Tool rooted in the checkout.
func clone(t *testing.T) (Tool, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	base := t.TempDir()
	origin := filepath.Join(base, "origin.git")
	seed := filepath.Join(base, "seed")
	main := filepath.Join(base, "engine")
	git := func(dir string, args ...string) {
		t.Helper()
		args = append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)
		if out, err := repo.Exec(dir, "git", args...); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	}
	git(base, "init", "-q", "--bare", "-b", "main", origin)
	git(base, "init", "-q", "-b", "main", seed)
	writeFile(t, filepath.Join(seed, "go.mod"), "module m\n")
	git(seed, "add", ".")
	git(seed, "commit", "-q", "-m", "seed")
	git(seed, "push", "-q", origin, "main")
	git(base, "clone", "-q", origin, main)

	var stdout, stderr bytes.Buffer
	return Tool{Root: main, Run: repo.Exec, Stdout: &stdout, Stderr: &stderr}, &stdout, &stderr
}

func TestWorktreeCreateAndRelease(t *testing.T) {
	tool, stdout, stderr := clone(t)
	dir := WorktreePath(tool.Root, "feat/x")
	if want := filepath.Join(filepath.Dir(tool.Root), "engine-worktrees", "feat-x"); dir != want {
		t.Fatalf("WorktreePath = %s, want %s", dir, want)
	}
	if code := tool.Worktree([]string{"create", "feat/x"}); code != 0 {
		t.Fatalf("create: %d\n%s", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		t.Fatalf("worktree not populated: %v", err)
	}

	// Release works from inside the worktree it removes.
	inside := tool
	inside.Root = dir
	if code := inside.Worktree([]string{"release", "feat/x"}); code != 0 {
		t.Fatalf("release: %d\n%s", code, stderr)
	}
	want(t, 0, 0, stdout, "removed worktree", "deleted branch feat/x")
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("worktree still exists: %v", err)
	}
}

func TestWorktreeKeepsUnmergedBranch(t *testing.T) {
	tool, stdout, stderr := clone(t)
	if code := tool.Worktree([]string{"create", "feat/y"}); code != 0 {
		t.Fatalf("create: %d\n%s", code, stderr)
	}
	dir := WorktreePath(tool.Root, "feat/y")
	writeFile(t, filepath.Join(dir, "new.txt"), "x\n")
	for _, args := range [][]string{{"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "wip"}} {
		if out, err := repo.Exec(dir, "git", args...); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	}
	if code := tool.Worktree([]string{"release", "feat/y"}); code != 0 {
		t.Fatalf("release: %d\n%s", code, stderr)
	}
	want(t, 0, 0, stdout, "kept branch feat/y")
}

func TestWorktreeRefusals(t *testing.T) {
	tool, _, stderr := clone(t)
	want(t, tool.Worktree([]string{"create"}), 2, stderr, "usage: ./dev worktree")
	want(t, tool.Worktree([]string{"release", "nope"}), 1, stderr, "no worktree has branch nope")
	want(t, tool.Worktree([]string{"release", "main"}), 1, stderr, "is the main checkout")

	if code := tool.Worktree([]string{"create", "feat/z"}); code != 0 {
		t.Fatalf("create: %d\n%s", code, stderr)
	}
	writeFile(t, filepath.Join(WorktreePath(tool.Root, "feat/z"), "dirty.txt"), "x\n")
	want(t, tool.Worktree([]string{"release", "feat/z"}), 1, stderr, "worktree kept")
	want(t, tool.Worktree([]string{"create", "feat/z"}), 1, stderr, "dev:")

	outside := tool
	outside.Root = t.TempDir()
	want(t, outside.Worktree([]string{"create", "a"}), 1, stderr, "dev:")
}

func TestDefaultBranchNeedsARemote(t *testing.T) {
	tool, _, stderr := clone(t)
	if out, err := repo.Exec(tool.Root, "git", "remote", "remove", "origin"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want(t, tool.Worktree([]string{"create", "b"}), 1, stderr, "dev:")
}
