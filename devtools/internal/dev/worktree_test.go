package dev

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinoderek/engine/devtools/internal/repo"
)

// clone makes a bare origin with one commit on main and a main checkout
// cloned from it, and returns a Tool rooted in the checkout. git is real; gh
// is faked and reports no PR.
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
	run := func(dir, name string, args ...string) (string, error) {
		if name == "gh" {
			return "no pull requests found", errors.New("exit 1")
		}
		return repo.Exec(dir, name, args...)
	}
	return Tool{Root: main, Run: run, Stdout: &stdout, Stderr: &stderr}, &stdout, &stderr
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
	commit(t, dir, "new.txt")
	gitIn(t, dir, "push", "-q", "origin", "feat/y")
	if code := tool.Worktree([]string{"release", "feat/y"}); code != 0 {
		t.Fatalf("release: %d\n%s", code, stderr)
	}
	want(t, 0, 0, stdout, "kept branch feat/y: it is not merged")
	if remoteTip(t, tool, "feat/y") == "" {
		t.Error("remote branch of a kept branch was deleted")
	}
}

func TestWorktreeReleaseDeletesThePushedBranch(t *testing.T) {
	tool, stdout, stderr := clone(t)
	if code := tool.Worktree([]string{"create", "feat/p"}); code != 0 {
		t.Fatalf("create: %d\n%s", code, stderr)
	}
	dir := WorktreePath(tool.Root, "feat/p")
	commit(t, dir, "p.txt")
	gitIn(t, dir, "push", "-q", "-u", "origin", "feat/p")
	if code := tool.Worktree([]string{"release", "feat/p"}); code != 0 {
		t.Fatalf("release: %d\n%s", code, stderr)
	}
	want(t, 0, 0, stdout, "deleted branch feat/p\n", "deleted remote branch feat/p")
	if _, err := repo.Exec(tool.Root, "git", "rev-parse", "--verify", "refs/heads/feat/p"); err == nil {
		t.Error("local branch survived")
	}
	if tip := remoteTip(t, tool, "feat/p"); tip != "" {
		t.Errorf("remote branch survived at %s", tip)
	}
}

func TestWorktreeReleaseKeepsAMovedRemoteBranch(t *testing.T) {
	tool, stdout, stderr := clone(t)
	if code := tool.Worktree([]string{"create", "feat/m"}); code != 0 {
		t.Fatalf("create: %d\n%s", code, stderr)
	}
	dir := WorktreePath(tool.Root, "feat/m")
	commit(t, dir, "m1.txt")
	gitIn(t, dir, "push", "-q", "-u", "origin", "feat/m")
	// Someone else's commit lands on origin; the local branch stays behind it,
	// so git branch -d still sees it as merged into its upstream.
	moved := commit(t, dir, "m2.txt")
	gitIn(t, dir, "push", "-q", "origin", "feat/m")
	gitIn(t, dir, "reset", "-q", "--hard", "HEAD~1")
	if code := tool.Worktree([]string{"release", "feat/m"}); code != 0 {
		t.Fatalf("release: %d\n%s", code, stderr)
	}
	want(t, 0, 0, stdout, "deleted branch feat/m\n")
	if strings.Contains(stdout.String(), "deleted remote branch") {
		t.Errorf("reported a remote delete:\n%s", stdout)
	}
	if tip := remoteTip(t, tool, "feat/m"); tip != moved {
		t.Errorf("remote branch at %q, want it kept at %s", tip, moved)
	}
}

// commit adds file to dir's branch in a new commit and returns its sha.
func commit(t *testing.T, dir, file string) string {
	t.Helper()
	writeFile(t, filepath.Join(dir, file), file+"\n")
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-q", "-m", file)
	return gitIn(t, dir, "rev-parse", "HEAD")
}

// remoteTip is the sha of branch on the tool's origin, or "" when it has none.
func remoteTip(t *testing.T, tool Tool, branch string) string {
	t.Helper()
	fields := strings.Fields(gitIn(t, tool.Root, "ls-remote", "origin", "refs/heads/"+branch))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	args = append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)
	out, err := repo.Exec(dir, "git", args...)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return strings.TrimSpace(out)
}

func TestWorktreeDeletesASquashMergedBranch(t *testing.T) {
	tool, stdout, stderr := clone(t)
	if code := tool.Worktree([]string{"create", "feat/s"}); code != 0 {
		t.Fatalf("create: %d\n%s", code, stderr)
	}
	dir := WorktreePath(tool.Root, "feat/s")
	writeFile(t, filepath.Join(dir, "s.txt"), "x\n")
	for _, args := range [][]string{{"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "s"}, {"push", "-q", "origin", "feat/s"}} {
		if out, err := repo.Exec(dir, "git", args...); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	}
	head, err := repo.Exec(dir, "git", "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	ghSays := func(answer string) {
		gitOnly := tool.Run
		tool.Run = func(dir, name string, args ...string) (string, error) {
			if name == "gh" {
				return answer, nil
			}
			return gitOnly(dir, name, args...)
		}
	}
	ghSays("MERGED " + strings.TrimSpace(head) + "\n")
	if code := tool.Worktree([]string{"release", "feat/s"}); code != 0 {
		t.Fatalf("release: %d\n%s", code, stderr)
	}
	want(t, 0, 0, stdout, "deleted branch feat/s: its PR merged it", "deleted remote branch feat/s")
}

func TestWorktreeKeepsCommitsAfterTheMergedHead(t *testing.T) {
	tool, stdout, stderr := clone(t)
	if code := tool.Worktree([]string{"create", "feat/t"}); code != 0 {
		t.Fatalf("create: %d\n%s", code, stderr)
	}
	dir := WorktreePath(tool.Root, "feat/t")
	writeFile(t, filepath.Join(dir, "t.txt"), "x\n")
	for _, args := range [][]string{{"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "after"}} {
		if out, err := repo.Exec(dir, "git", args...); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	}
	gitOnly := tool.Run
	tool.Run = func(dir, name string, args ...string) (string, error) {
		if name == "gh" {
			return "MERGED 0123456789abcdef0123456789abcdef01234567\n", nil
		}
		return gitOnly(dir, name, args...)
	}
	if code := tool.Worktree([]string{"release", "feat/t"}); code != 0 {
		t.Fatalf("release: %d\n%s", code, stderr)
	}
	want(t, 0, 0, stdout, "kept branch feat/t: it has commits after its merged PR (0123456)")
	if _, err := repo.Exec(tool.Root, "git", "rev-parse", "--verify", "refs/heads/feat/t"); err != nil {
		t.Errorf("branch was deleted: %v", err)
	}
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
