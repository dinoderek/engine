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
	gitIn(t, base, "init", "-q", "--bare", "-b", "main", origin)
	gitIn(t, base, "init", "-q", "-b", "main", seed)
	writeFile(t, filepath.Join(seed, "go.mod"), "module m\n")
	gitIn(t, seed, "add", ".")
	gitIn(t, seed, "commit", "-q", "-m", "seed")
	gitIn(t, seed, "push", "-q", origin, "main")
	gitIn(t, base, "clone", "-q", origin, main)

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

func TestWorktreeKeepsTheRemoteOfAnUnmergedPushedBranch(t *testing.T) {
	tool, stdout, stderr := clone(t)
	if code := tool.Worktree([]string{"create", "feat/u"}); code != 0 {
		t.Fatalf("create: %d\n%s", code, stderr)
	}
	dir := WorktreePath(tool.Root, "feat/u")
	tip := commit(t, dir, "u.txt")
	// With an upstream, git branch -d passes a branch that is only pushed.
	gitIn(t, dir, "push", "-q", "-u", "origin", "feat/u")
	if code := tool.Worktree([]string{"release", "feat/u"}); code != 0 {
		t.Fatalf("release: %d\n%s", code, stderr)
	}
	if strings.Contains(stdout.String(), "deleted remote branch") {
		t.Errorf("reported a remote delete:\n%s", stdout)
	}
	if got := remoteTip(t, tool, "feat/u"); got != tip {
		t.Errorf("remote branch at %q, want it kept at %s", got, tip)
	}
}

func TestWorktreeDeletesTheRemoteOfAMergedBranchWithAnUpstream(t *testing.T) {
	tool, stdout, stderr := clone(t)
	if code := tool.Worktree([]string{"create", "feat/p"}); code != 0 {
		t.Fatalf("create: %d\n%s", code, stderr)
	}
	dir := WorktreePath(tool.Root, "feat/p")
	tip := commit(t, dir, "p.txt")
	gitIn(t, dir, "push", "-q", "-u", "origin", "feat/p")
	ghSays(&tool, "MERGED "+tip+"\n")
	if code := tool.Worktree([]string{"release", "feat/p"}); code != 0 {
		t.Fatalf("release: %d\n%s", code, stderr)
	}
	want(t, 0, 0, stdout, "deleted branch feat/p\n", "deleted remote branch feat/p")
	if _, err := repo.Exec(tool.Root, "git", "rev-parse", "--verify", "refs/heads/feat/p"); err == nil {
		t.Error("local branch survived")
	}
	if got := remoteTip(t, tool, "feat/p"); got != "" {
		t.Errorf("remote branch survived at %s", got)
	}
}

func TestWorktreeKeepsAMovedRemoteBranch(t *testing.T) {
	tool, stdout, stderr := clone(t)
	if code := tool.Worktree([]string{"create", "feat/m"}); code != 0 {
		t.Fatalf("create: %d\n%s", code, stderr)
	}
	dir := WorktreePath(tool.Root, "feat/m")
	tip := commit(t, dir, "m1.txt")
	gitIn(t, dir, "push", "-q", "-u", "origin", "feat/m")
	// A later commit lands on origin after the PR merged tip; the local
	// branch stays at tip, which git branch -d sees as merged upstream.
	moved := commit(t, dir, "m2.txt")
	gitIn(t, dir, "push", "-q", "origin", "feat/m")
	gitIn(t, dir, "reset", "-q", "--hard", tip)
	ghSays(&tool, "MERGED "+tip+"\n")
	if code := tool.Worktree([]string{"release", "feat/m"}); code != 0 {
		t.Fatalf("release: %d\n%s", code, stderr)
	}
	want(t, 0, 0, stdout, "deleted branch feat/m\n")
	if strings.Contains(stdout.String(), "deleted remote branch") {
		t.Errorf("reported a remote delete:\n%s", stdout)
	}
	if got := remoteTip(t, tool, "feat/m"); got != moved {
		t.Errorf("remote branch at %q, want it kept at %s", got, moved)
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

// ghSays makes the tool's gh print answer; git stays real.
func ghSays(tool *Tool, answer string) {
	gitOnly := tool.Run
	tool.Run = func(dir, name string, args ...string) (string, error) {
		if name == "gh" {
			return answer, nil
		}
		return gitOnly(dir, name, args...)
	}
}

// gitIn runs git in dir as a test identity and returns its trimmed output.
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
	tip := commit(t, dir, "s.txt")
	gitIn(t, dir, "push", "-q", "origin", "feat/s")
	ghSays(&tool, "MERGED "+tip+"\n")
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
	commit(t, dir, "t.txt")
	ghSays(&tool, "MERGED 0123456789abcdef0123456789abcdef01234567\n")
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
