package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// worktreeCommand is the script a repo provides for creating a task
// worktree: `<clone>/dev worktree create <branch>`.
const worktreeCommand = "dev"

// Worktree is a task's worktree.
type Worktree struct {
	Path   string
	Branch string
}

// Name is the session name of a task, <repo>-<task>: the tmux window, the
// remote-control name, and part of the branch.
func Name(repo Repo, task int) string {
	return fmt.Sprintf("%s-%d", repo.Name, task)
}

// Branch is the branch a task's worktree opens on. It carries the session
// name, so a session can be found from its branch.
func Branch(repo Repo, task int) string {
	return "task/" + Name(repo, task)
}

// CreateWorktree creates the worktree for a task in clone, branched from the
// remote's default branch. It uses the repo's own command, `./dev worktree
// create <branch>`, when the clone has an executable `dev` at its root, since
// that command also provisions what the task needs. Otherwise it runs
// `git worktree add`, beside the clone in <clone>-worktrees/. It refuses when
// the task's branch or worktree already exists, so it never reuses leftovers.
func (r Repos) CreateWorktree(clone Clone, task int) (Worktree, error) {
	if task < 1 {
		return Worktree{}, fmt.Errorf("task number must be positive, got %d", task)
	}
	branch := Branch(clone.Repo, task)
	unlock, err := lock(clone.Path + ".lock")
	if err != nil {
		return Worktree{}, err
	}
	defer unlock()
	if err := r.refuseExisting(clone, branch); err != nil {
		return Worktree{}, err
	}
	if script := filepath.Join(clone.Path, worktreeCommand); isExecutable(script) {
		_, err = r.Run(clone.Path, r.env(), script, "worktree", "create", branch)
	} else {
		dir := clone.Path + "-worktrees/" + strings.ReplaceAll(branch, "/", "-")
		_, err = r.git(clone.Path, "worktree", "add", "--quiet", "--no-track", "-b", branch, dir, "origin/"+clone.Default)
	}
	if err != nil {
		return Worktree{}, fmt.Errorf("create the worktree for %s: %w", branch, err)
	}
	path, err := r.worktreeOf(clone, branch)
	if err != nil {
		return Worktree{}, err
	}
	if path == "" {
		return Worktree{}, fmt.Errorf("the worktree command ran but no worktree has branch %s", branch)
	}
	return Worktree{Path: path, Branch: branch}, nil
}

// refuseExisting stops when the branch is already taken, saying where.
func (r Repos) refuseExisting(clone Clone, branch string) error {
	path, err := r.worktreeOf(clone, branch)
	if err != nil {
		return err
	}
	if path != "" {
		return fmt.Errorf("branch %s already has a worktree at %s; release it first", branch, path)
	}
	if _, err := r.git(clone.Path, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		return fmt.Errorf("branch %s already exists in %s with no worktree; delete it or check it out first", branch, clone.Path)
	}
	return nil
}

// worktreeOf returns the path of the worktree that has branch checked out,
// or "" when none has.
func (r Repos) worktreeOf(clone Clone, branch string) (string, error) {
	out, err := r.git(clone.Path, "worktree", "list", "--porcelain")
	if err != nil {
		return "", fmt.Errorf("list worktrees: %w", err)
	}
	path := ""
	for _, line := range strings.Split(out, "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			path = p
		}
		if line == "branch refs/heads/"+branch {
			return path, nil
		}
	}
	return "", nil
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0
}
