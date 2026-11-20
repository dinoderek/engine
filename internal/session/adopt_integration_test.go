package session

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The tests here run real git for a task whose earlier attempt left its
// branch only in engine's clone: a branch engine marked as the task's own.

// ownFixture is a synced clone whose origin has only main, and the task
// branch task/r-5 an earlier attempt, run r-5-a1, created there with a
// commit it never pushed. It returns that commit, and the worktree the
// attempt left.
func ownFixture(t *testing.T) (Repos, Clone, Worktree, string) {
	t.Helper()
	remote := newRemote(t, "main")
	remote.commit(t, "main", "a.txt", "a", 0o644)
	r := remote.repos(t)
	clone, err := r.Sync(mustRepo(t, "o/r"))
	if err != nil {
		t.Fatal(err)
	}
	first, err := r.CreateWorktree(clone, 5, false, "r-5-a1")
	if err != nil || !first.NewBranch || first.Adopted {
		t.Fatalf("first attempt: %+v, %v", first, err)
	}
	if err := os.WriteFile(filepath.Join(first.Path, "work.txt"), []byte("work"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, first.Path, "add", "work.txt")
	mustGit(t, first.Path, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "--quiet", "-m", "work")
	return r, clone, first, mustGit(t, first.Path, "rev-parse", "HEAD")
}

func TestCreateWorktreeAdoptsTheTasksOwnLocalBranch(t *testing.T) {
	r, clone, first, commit := ownFixture(t)
	mustGit(t, clone.Path, "worktree", "remove", first.Path)
	// The next attempt is no resume to the coordinator: the first never
	// opened a window. The branch's mark still makes it the task's own.
	wt, err := r.CreateWorktree(clone, 5, false, "r-5-a2")
	if err != nil {
		t.Fatal(err)
	}
	if !wt.Adopted || wt.NewBranch || wt.Head != commit {
		t.Errorf("worktree = %+v, want the branch adopted at %s", wt, commit)
	}
	if branch, head := headOf(t, wt); branch != "task/r-5" || head != commit {
		t.Errorf("worktree on %s at %s, want task/r-5 at %s", branch, head, commit)
	}
	// Undoing the second attempt's spawn keeps the branch: it holds work.
	if err := r.RemoveWorktree(clone, wt); err != nil {
		t.Fatal(err)
	}
	if got := mustGit(t, clone.Path, "rev-parse", "refs/heads/task/r-5"); got != commit {
		t.Errorf("branch at %s after undo, want %s", got, commit)
	}
}

func TestCreateWorktreeAdoptsTheTasksOwnWorktreeWithChanges(t *testing.T) {
	r, clone, first, commit := ownFixture(t)
	if err := os.WriteFile(filepath.Join(first.Path, "draft.txt"), []byte("half done"), 0o644); err != nil {
		t.Fatal(err)
	}
	wt, err := r.CreateWorktree(clone, 5, true, "r-5-a2")
	if err != nil {
		t.Fatal(err)
	}
	if !samePath(t, wt.Path, first.Path) || !wt.Adopted || wt.Created || wt.Head != commit {
		t.Errorf("worktree = %+v, want %s adopted at %s", wt, first.Path, commit)
	}
	if data, err := os.ReadFile(filepath.Join(wt.Path, "draft.txt")); err != nil || string(data) != "half done" {
		t.Errorf("the uncommitted change is gone: %q, %v", data, err)
	}
}

func TestCreateWorktreeRefusesAnUnmarkedBranch(t *testing.T) {
	r, clone, first, _ := ownFixture(t)
	mustGit(t, clone.Path, "worktree", "remove", first.Path)
	mustGit(t, clone.Path, "config", "--unset", "branch.task/r-5.engineRun")
	_, err := r.CreateWorktree(clone, 5, false, "r-5-a2")
	var takenErr *BranchTakenError
	if !errors.As(err, &takenErr) || !strings.Contains(err.Error(), "with commits of its own") {
		t.Fatalf("a branch no run of the task marked is never adopted: err = %v", err)
	}
	// Another task's mark does not make it this task's either.
	mustGit(t, clone.Path, "config", "branch.task/r-5.engineRun", "r-50-a9")
	if _, err := r.CreateWorktree(clone, 5, false, "r-5-a2"); !errors.As(err, &takenErr) {
		t.Fatalf("another task's mark: err = %v", err)
	}
}

func TestCreateWorktreeFollowsOriginOnlyWhenTheOwnBranchIsBehind(t *testing.T) {
	remote, r, clone, _ := resumeFixture(t, "")
	first, err := r.CreateWorktree(clone, 5, true, "r-5-a1")
	if err != nil {
		t.Fatal(err)
	}
	mustGit(t, clone.Path, "worktree", "remove", first.Path)
	// Behind origin, which moved on: fast-forwarded to origin's tip.
	newer := remote.commit(t, "task/r-5", "more.txt", "more", 0o644)
	if _, err := r.Sync(mustRepo(t, "o/r")); err != nil {
		t.Fatal(err)
	}
	wt, err := r.CreateWorktree(clone, 5, false, "r-5-a2")
	if err != nil || wt.Head != newer {
		t.Fatalf("worktree = %+v, %v; want origin's tip %s", wt, err, newer)
	}
	// Ahead of origin with its own commit: kept as it is.
	if err := os.WriteFile(filepath.Join(wt.Path, "own.txt"), []byte("own"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, wt.Path, "add", "own.txt")
	mustGit(t, wt.Path, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "--quiet", "-m", "own")
	own := mustGit(t, wt.Path, "rev-parse", "HEAD")
	mustGit(t, clone.Path, "worktree", "remove", wt.Path)
	wt, err = r.CreateWorktree(clone, 5, true, "r-5-a3")
	if err != nil || wt.Head != own {
		t.Fatalf("worktree = %+v, %v; want its own commit %s", wt, err, own)
	}
}

// ownOnOrigin is resumeFixture with the task's branch adopted by run
// r-5-a1, then given a commit of its own that origin lacks; origin then moves
// on too. It returns the worktree the attempt left.
func ownOnOrigin(t *testing.T) (Repos, Clone, Worktree) {
	t.Helper()
	remote, r, clone, _ := resumeFixture(t, "")
	first, err := r.CreateWorktree(clone, 5, true, "r-5-a1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(first.Path, "own.txt"), []byte("own"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, first.Path, "add", "own.txt")
	mustGit(t, first.Path, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "--quiet", "-m", "own")
	remote.commit(t, "task/r-5", "theirs.txt", "theirs", 0o644)
	if _, err := r.Sync(mustRepo(t, "o/r")); err != nil {
		t.Fatal(err)
	}
	return r, clone, first
}

func TestCreateWorktreeRefusesAnOwnBranchThatDivergedFromOrigin(t *testing.T) {
	r, clone, first := ownOnOrigin(t)
	var takenErr *BranchTakenError
	// The worktree it left, and the branch without it, are both refused.
	if _, err := r.CreateWorktree(clone, 5, true, "r-5-a2"); !errors.As(err, &takenErr) || !strings.Contains(err.Error(), "have each moved on") {
		t.Fatalf("a diverged worktree: err = %v", err)
	}
	mustGit(t, clone.Path, "worktree", "remove", first.Path)
	if _, err := r.CreateWorktree(clone, 5, true, "r-5-a2"); !errors.As(err, &takenErr) || !strings.Contains(err.Error(), "have each moved on") {
		t.Fatalf("a diverged branch: err = %v", err)
	}
}

func TestCreateWorktreeRefusesAChangedOwnWorktreeBehindOrigin(t *testing.T) {
	remote, r, clone, _ := resumeFixture(t, "")
	first, err := r.CreateWorktree(clone, 5, true, "r-5-a1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(first.Path, "draft.txt"), []byte("half done"), 0o644); err != nil {
		t.Fatal(err)
	}
	remote.commit(t, "task/r-5", "theirs.txt", "theirs", 0o644)
	if _, err := r.Sync(mustRepo(t, "o/r")); err != nil {
		t.Fatal(err)
	}
	var takenErr *BranchTakenError
	if _, err := r.CreateWorktree(clone, 5, true, "r-5-a2"); !errors.As(err, &takenErr) || !strings.Contains(err.Error(), "with uncommitted changes, and origin's branch has commits it lacks") {
		t.Fatalf("err = %v", err)
	}
}

func TestRemoveWorktreeKeepsAnAdoptedBranchTheReposReleaseDeleted(t *testing.T) {
	r, clone, first, commit := ownFixture(t)
	mustGit(t, clone.Path, "worktree", "remove", first.Path)
	wt, err := r.CreateWorktree(clone, 5, false, "r-5-a2")
	if err != nil || !wt.Created || wt.NewBranch {
		t.Fatalf("worktree = %+v, %v", wt, err)
	}
	// A repo whose own release removes the worktree and force-deletes the
	// branch, as a spawn's undo would run it.
	release := "#!/bin/sh\n[ $# -eq 0 ] && { echo 'usage: dev worktree release <branch>'; exit 2; }\n" +
		"git worktree remove --force \"" + wt.Path + "\" && git branch -D \"$3\"\n"
	if err := os.WriteFile(filepath.Join(clone.Path, "dev"), []byte(release), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := r.RemoveWorktree(clone, wt); err != nil {
		t.Fatal(err)
	}
	if got := mustGit(t, clone.Path, "rev-parse", "refs/heads/task/r-5"); got != commit {
		t.Errorf("branch at %q after the repo's release, want it restored at %s", got, commit)
	}
}
