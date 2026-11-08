package session

import (
	"errors"
	"strings"
	"testing"
)

// The tests here run real git, as integration_test.go does, for a task whose
// branch origin already has: the work of an earlier attempt.

// resumeFixture is a synced clone whose origin has main and the task's branch
// task/r-5, one commit ahead of main. It returns the branch's tip.
func resumeFixture(t *testing.T, dev string) (remoteFixture, Repos, Clone, string) {
	t.Helper()
	remote := newRemote(t, "main")
	remote.commit(t, "main", "a.txt", "a", 0o644)
	if dev != "" {
		remote.commit(t, "main", "dev", dev, 0o755)
	}
	tip := remote.commit(t, "task/r-5", "work.txt", "work", 0o644)
	r := remote.repos(t)
	clone, err := r.Sync(mustRepo(t, "o/r"))
	if err != nil {
		t.Fatal(err)
	}
	return remote, r, clone, tip
}

func headOf(t *testing.T, wt Worktree) (branch, commit string) {
	t.Helper()
	return mustGit(t, wt.Path, "rev-parse", "--abbrev-ref", "HEAD"), mustGit(t, wt.Path, "rev-parse", "HEAD")
}

func TestCreateWorktreeRefusesOriginsBranchOnAFirstAttempt(t *testing.T) {
	_, r, clone, _ := resumeFixture(t, "")
	_, err := r.CreateWorktree(clone, 5, false)
	var takenErr *BranchTakenError
	if !errors.As(err, &takenErr) || !strings.Contains(err.Error(), "origin already has branch task/r-5") {
		t.Fatalf("a stray branch must never be adopted: err = %v", err)
	}
	if _, err := realGit(clone.Path, nil, "git", "rev-parse", "--verify", "--quiet", "refs/heads/task/r-5"); err == nil {
		t.Error("the refusal made a local branch")
	}
}

func TestCreateWorktreeResumesFromOriginsBranch(t *testing.T) {
	remote, r, clone, tip := resumeFixture(t, "")
	wt, err := r.CreateWorktree(clone, 5, true)
	if err != nil {
		t.Fatal(err)
	}
	if !samePath(t, wt.Path, clone.Path+"-worktrees/task-r-5") {
		t.Errorf("worktree = %+v, want git's fallback path", wt)
	}
	if branch, head := headOf(t, wt); branch != "task/r-5" || head != tip {
		t.Errorf("worktree on %s at %s, want task/r-5 at origin's tip %s", branch, head, tip)
	}
	// The next attempt finds the local branch behind origin, which has moved
	// on since: it is fast-forwarded to origin's tip.
	mustGit(t, clone.Path, "worktree", "remove", wt.Path)
	newer := remote.commit(t, "task/r-5", "more.txt", "more", 0o644)
	if _, err := r.Sync(mustRepo(t, "o/r")); err != nil {
		t.Fatal(err)
	}
	wt, err = r.CreateWorktree(clone, 5, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, head := headOf(t, wt); head != newer {
		t.Errorf("worktree at %s, want origin's new tip %s", head, newer)
	}
}

func TestCreateWorktreeRefusesALocalBranchAheadOfOrigin(t *testing.T) {
	_, r, clone, tip := resumeFixture(t, "")
	mustGit(t, clone.Path, "branch", "task/r-5", "origin/task/r-5")
	mustGit(t, clone.Path, "worktree", "add", "--quiet", clone.Path+"-wt", "task/r-5")
	mustGit(t, clone.Path+"-wt", "commit", "--quiet", "--allow-empty", "-m", "unpushed")
	mustGit(t, clone.Path, "worktree", "remove", clone.Path+"-wt")
	local := mustGit(t, clone.Path, "rev-parse", "task/r-5")
	_, err := r.CreateWorktree(clone, 5, true)
	var takenErr *BranchTakenError
	if !errors.As(err, &takenErr) || !strings.Contains(err.Error(), "has commits that origin lacks") {
		t.Fatalf("err = %v", err)
	}
	if got := mustGit(t, clone.Path, "rev-parse", "task/r-5"); got != local || got == tip {
		t.Errorf("the refusal moved the local branch to %s", got)
	}
}

func TestCreateWorktreeResumesWithTheReposOwnCommand(t *testing.T) {
	// A repo's command that checks out a local branch that exists as it is,
	// and otherwise branches from the default branch.
	script := "#!/bin/sh\nset -e\ncd \"$(dirname \"$0\")\"\n" +
		"if [ $# -eq 0 ]; then echo 'usage: ./dev worktree create <branch>' >&2; exit 2; fi\n" +
		"if git show-ref --verify --quiet \"refs/heads/$3\"; then git worktree add --quiet \"$PWD-custom\" \"$3\"\n" +
		"else git worktree add --quiet --no-track -b \"$3\" \"$PWD-custom\" origin/main; fi\n"
	_, r, clone, tip := resumeFixture(t, script)
	wt, err := r.CreateWorktree(clone, 5, true)
	if err != nil {
		t.Fatal(err)
	}
	if !samePath(t, wt.Path, clone.Path+"-custom") {
		t.Errorf("worktree = %+v, want the repo's own path", wt)
	}
	if branch, head := headOf(t, wt); branch != "task/r-5" || head != tip {
		t.Errorf("worktree on %s at %s, want task/r-5 at origin's tip %s", branch, head, tip)
	}
}
