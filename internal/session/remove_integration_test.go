package session

import (
	"os"
	"testing"
)

// RemoveWorktree, with real git, removes only what CreateWorktree made: a
// new worktree and its new branch, a resumed worktree but not the branch
// origin had, and nothing of a worktree it adopted.
func TestRemoveWorktreeRemovesOnlyWhatCreateMade(t *testing.T) {
	remote := newRemote(t, "main")
	remote.commit(t, "main", "a.txt", "a", 0o644)
	remote.commit(t, "task/r-5", "work.txt", "work", 0o644)
	r := remote.repos(t)
	clone, err := r.Sync(mustRepo(t, "o/r"))
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := r.CreateWorktree(clone, 4, false)
	if err != nil || !fresh.Created || !fresh.NewBranch {
		t.Fatalf("a first attempt: %+v, %v", fresh, err)
	}
	if err := r.RemoveWorktree(clone, fresh); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(fresh.Path); !os.IsNotExist(err) {
		t.Errorf("the new worktree is still at %s", fresh.Path)
	}
	if _, err := realGit(clone.Path, nil, "git", "rev-parse", "--verify", "--quiet", "refs/heads/task/r-4"); err == nil {
		t.Error("the new branch was kept")
	}

	resumed, err := r.CreateWorktree(clone, 5, true)
	if err != nil || !resumed.Created || resumed.NewBranch {
		t.Fatalf("a resume from origin's branch: %+v, %v", resumed, err)
	}
	if err := r.RemoveWorktree(clone, resumed); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(resumed.Path); !os.IsNotExist(err) {
		t.Errorf("the resumed worktree is still at %s", resumed.Path)
	}
	mustGit(t, clone.Path, "rev-parse", "--verify", "--quiet", "refs/heads/task/r-5")

	left, err := r.CreateWorktree(clone, 5, true)
	if err != nil {
		t.Fatal(err)
	}
	adopted, err := r.CreateWorktree(clone, 5, true)
	if err != nil || adopted.Created {
		t.Fatalf("an adopted worktree: %+v, %v", adopted, err)
	}
	if err := r.RemoveWorktree(clone, adopted); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(left.Path); err != nil {
		t.Errorf("the adopted worktree was removed: %v", err)
	}
}
