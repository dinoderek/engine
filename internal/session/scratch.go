package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Head is the commit the clone's origin has for its default branch, as the
// last Sync fetched it.
func (r Repos) Head(clone Clone) (string, error) {
	head, err := r.git(clone.Path, "rev-parse", "--verify", "origin/"+clone.Default+"^{commit}")
	if err == nil && head == "" {
		err = errors.New("git printed no commit")
	}
	if err != nil {
		return "", fmt.Errorf("read origin/%s in %s: %w", clone.Default, clone.Path, err)
	}
	return head, nil
}

// Origin is the remote an existing clone of repo fetches from.
func (r Repos) Origin(repo Repo) (string, error) {
	path, err := r.Path(repo)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		return "", fmt.Errorf("no clone of %s at %s", repo, path)
	}
	url, err := r.git(path, "config", "--get", "remote.origin.url")
	if err != nil || url == "" {
		return "", fmt.Errorf("the clone of %s at %s has no origin", repo, path)
	}
	return url, nil
}

// AddScratch checks commit out, detached, in a throwaway worktree of clone at
// dir, replacing whatever an earlier one left there. It takes the clone's
// lock, as Sync does.
func (r Repos) AddScratch(clone Clone, dir, commit string) error {
	unlock, err := lock(clone.Path + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	if err := r.dropScratch(clone, dir); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	if _, err := r.git(clone.Path, "worktree", "add", "--quiet", "--detach", dir, commit); err != nil {
		return fmt.Errorf("add a scratch worktree of %s at %s: %w", commit, dir, err)
	}
	return nil
}

// RemoveScratch removes the throwaway worktree at dir, and clone's record of
// it. It takes the clone's lock.
func (r Repos) RemoveScratch(clone Clone, dir string) error {
	unlock, err := lock(clone.Path + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	return r.dropScratch(clone, dir)
}

// dropScratch removes dir and prunes clone's worktree list, so a worktree
// whose directory is gone leaves no record behind. Callers hold the lock.
func (r Repos) dropScratch(clone Clone, dir string) error {
	if _, err := os.Stat(dir); err == nil {
		// A dirty or locked worktree is removed too: nothing in it is kept.
		_, _ = r.git(clone.Path, "worktree", "remove", "--force", "--force", dir)
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("remove the scratch worktree %s: %w", dir, err)
		}
	}
	if _, err := r.git(clone.Path, "worktree", "prune"); err != nil {
		return fmt.Errorf("prune the worktrees of %s: %w", clone.Path, err)
	}
	return nil
}
