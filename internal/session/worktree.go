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

// TaskName is the session name of a task, <repo>-<task>: the tmux window, the
// remote-control name, and part of the branch.
func TaskName(repo Repo, task int) string {
	return fmt.Sprintf("%s-%d", repo.Name, task)
}

// Branch is the branch a task's worktree opens on. It carries the session
// name, so a session can be found from its branch.
func Branch(repo Repo, task int) string {
	return "task/" + TaskName(repo, task)
}

// CreateWorktree creates the worktree for a task in clone, branched from the
// remote's default branch. It uses the repo's own command, `./dev worktree
// create <branch>`, when the clone has an executable `dev` at its root whose
// usage lists that verb, since that command also provisions what the task
// needs. Otherwise it runs `git worktree add`, beside the clone in
// <clone>-worktrees/. It refuses when the task's worktree already exists, or
// its branch holds work, so it never reuses leftovers.
func (r Repos) CreateWorktree(clone Clone, task int) (Worktree, error) {
	if task < 1 {
		return Worktree{}, fmt.Errorf("task number must be positive, got %d", task)
	}
	if err := r.checkToken(); err != nil {
		return Worktree{}, err
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
	if script := filepath.Join(clone.Path, worktreeCommand); r.createsWorktrees(clone, script) {
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

// BranchTakenError is a worktree CreateWorktree did not create, changing
// nothing, because its branch is already in use.
type BranchTakenError struct{ Reason string }

func (e *BranchTakenError) Error() string { return e.Reason }

func taken(format string, args ...any) error {
	return &BranchTakenError{Reason: fmt.Sprintf(format, args...)}
}

// refuseExisting stops when the branch is already taken, saying where. A
// local branch with no worktree and no commits of its own, such as one left
// by a session that ended before it did any work, is deleted instead, so
// the task can start again.
func (r Repos) refuseExisting(clone Clone, branch string) error {
	path, err := r.worktreeOf(clone, branch)
	if err != nil {
		return err
	}
	if path != "" {
		return taken("branch %s already has a worktree at %s; release it first", branch, path)
	}
	if _, err := r.git(clone.Path, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+branch); err == nil {
		return taken("origin already has branch %s, from an earlier run; delete it or continue that work by hand", branch)
	}
	if _, err := r.git(clone.Path, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err != nil {
		return nil
	}
	if _, err := r.git(clone.Path, "merge-base", "--is-ancestor", "refs/heads/"+branch, "refs/remotes/origin/"+clone.Default); err != nil {
		return taken("branch %s already exists in %s with commits of its own and no worktree; delete it or check it out first", branch, clone.Path)
	}
	if _, err := r.git(clone.Path, "branch", "-D", branch); err != nil {
		return fmt.Errorf("delete the leftover branch %s, which has no commits of its own: %w", branch, err)
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

// createsWorktrees reports whether script is a `dev` that provides `worktree
// create`. A repo may have its own unrelated `dev`, so being executable is not
// enough: run with no verb, a `dev` prints its usage, which lists the verbs it
// has. Its exit status is ignored, since printing usage usually fails.
func (r Repos) createsWorktrees(clone Clone, script string) bool {
	if !isExecutable(script) {
		return false
	}
	usage, _ := r.Run(clone.Path, r.env(), script)
	return strings.Contains(usage, "worktree create")
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0
}
