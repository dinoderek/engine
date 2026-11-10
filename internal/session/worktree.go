package session

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// worktreeCommand is the script a repo provides for creating and releasing
// a task worktree: `<clone>/dev worktree create|release <branch>`.
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
// <clone>-worktrees/. Except on resume, below, it refuses when the task's
// worktree already exists, or its branch holds work, so it never reuses
// leftovers.
//
// resume is the caller's word that the task had an earlier attempt. Then a
// branch that origin has is the task's own work: the worktree opens on it, at
// origin's tip, rather than refusing. A local branch behind origin is
// fast-forwarded; one with commits origin lacks is still refused. A worktree
// the earlier attempt left on the branch is adopted as it is, fast-forwarded
// to origin's tip, when it is clean and holds no commits origin lacks;
// otherwise it is refused, so that work is never discarded.
func (r Repos) CreateWorktree(clone Clone, task int, resume bool) (Worktree, error) {
	if task < 1 {
		return Worktree{}, fmt.Errorf("task number must be positive, got %d", task)
	}
	branch := Branch(clone.Repo, task)
	unlock, err := lock(clone.Path + ".lock")
	if err != nil {
		return Worktree{}, err
	}
	defer unlock()
	adopt, existing, err := r.prepareBranch(clone, branch, resume)
	if err != nil {
		return Worktree{}, err
	}
	if existing != "" {
		return Worktree{Path: existing, Branch: branch}, nil
	}
	if script := filepath.Join(clone.Path, worktreeCommand); r.provides(clone.Path, script, "worktree create") {
		// The repo's command checks out a local branch that exists as it is.
		_, err = r.Run(clone.Path, []string{"GIT_TERMINAL_PROMPT=0"}, script, "worktree", "create", branch)
	} else {
		dir := clone.Path + "-worktrees/" + strings.ReplaceAll(branch, "/", "-")
		args := []string{"worktree", "add", "--quiet", "--no-track", "-b", branch, dir, "origin/" + clone.Default}
		if adopt {
			args = []string{"worktree", "add", "--quiet", dir, branch}
		}
		_, err = r.git(clone.Path, args...)
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
	if adopt {
		if err := r.checkAtOrigin(clone, path, branch); err != nil {
			return Worktree{}, err
		}
	}
	return Worktree{Path: path, Branch: branch}, nil
}

// checkAtOrigin fails when a resumed worktree is not at origin's tip of its
// branch, as when a repo's command made the branch anew rather than checking
// it out as it is.
func (r Repos) checkAtOrigin(clone Clone, path, branch string) error {
	head, err := r.git(path, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("read the head of %s: %w", path, err)
	}
	tip, err := r.git(clone.Path, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+branch)
	if err != nil || strings.TrimSpace(head) != strings.TrimSpace(tip) {
		return fmt.Errorf("the worktree at %s is not at origin's tip of %s; the repo's worktree command must check out an existing branch as it is", path, branch)
	}
	return nil
}

// BranchTakenError is a worktree CreateWorktree did not create, changing
// nothing, because its branch is already in use.
type BranchTakenError struct{ Reason string }

func (e *BranchTakenError) Error() string { return e.Reason }

func taken(format string, args ...any) error {
	return &BranchTakenError{Reason: fmt.Sprintf(format, args...)}
}

// prepareBranch stops when the branch is already taken, saying where. A
// local branch with no worktree and no commits of its own, such as one left
// by a session that ended before it did any work, is deleted instead, so
// the task can start again. On resume, a branch that origin has is set to
// origin's tip locally instead, and adopt says the worktree opens on it; a
// worktree already on the branch is adopted, and existing is its path.
func (r Repos) prepareBranch(clone Clone, branch string, resume bool) (adopt bool, existing string, err error) {
	path, err := r.worktreeOf(clone, branch)
	if err != nil {
		return false, "", err
	}
	if path != "" {
		if !resume {
			return false, "", taken("branch %s already has a worktree at %s; release it first", branch, path)
		}
		return false, path, r.adoptWorktree(clone, branch, path)
	}
	if _, err := r.git(clone.Path, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+branch); err == nil {
		if !resume {
			return false, "", taken("origin already has branch %s, from an earlier run; delete it or continue that work by hand", branch)
		}
		return true, "", r.followOrigin(clone, branch)
	}
	if _, err := r.git(clone.Path, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err != nil {
		return false, "", nil
	}
	if _, err := r.git(clone.Path, "merge-base", "--is-ancestor", "refs/heads/"+branch, "refs/remotes/origin/"+clone.Default); err != nil {
		return false, "", taken("branch %s already exists in %s with commits of its own and no worktree; delete it or check it out first", branch, clone.Path)
	}
	if _, err := r.git(clone.Path, "branch", "-D", branch); err != nil {
		return false, "", fmt.Errorf("delete the leftover branch %s, which has no commits of its own: %w", branch, err)
	}
	return false, "", nil
}

// adoptWorktree readies a worktree an earlier attempt left on branch for a
// resume: it fast-forwards it to origin's tip of the branch, or of the
// default branch when origin lacks it. It refuses a worktree with
// uncommitted changes, untracked files included, or with commits that tip
// lacks, which moving it would lose. It refuses engine's clone itself, and a
// worktree whose folder is gone, which `git worktree prune` clears. Ignored
// files do not block adoption, and a fast-forward may replace one, as any
// checkout does.
func (r Repos) adoptWorktree(clone Clone, branch, path string) error {
	if filepath.Clean(path) == filepath.Clean(clone.Path) {
		return taken("branch %s is checked out in engine's clone %s; check out its default branch there first", branch, path)
	}
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return taken("branch %s has a worktree at %s whose folder is gone; run git -C %s worktree prune first", branch, path, clone.Path)
	}
	status, err := r.git(path, "status", "--porcelain")
	if err != nil {
		return fmt.Errorf("read the status of the worktree at %s: %w", path, err)
	}
	if strings.TrimSpace(status) != "" {
		return taken("branch %s already has a worktree at %s with uncommitted changes; commit and push them, or remove them, first", branch, path)
	}
	tip := "refs/remotes/origin/" + branch
	if _, err := r.git(clone.Path, "rev-parse", "--verify", "--quiet", tip); err != nil {
		tip = "refs/remotes/origin/" + clone.Default
	}
	if _, err := r.git(path, "merge-base", "--is-ancestor", "HEAD", tip); err != nil {
		return taken("branch %s already has a worktree at %s with commits that origin lacks; push or delete them first", branch, path)
	}
	if _, err := r.git(path, "merge", "--ff-only", "--quiet", tip); err != nil {
		return fmt.Errorf("fast-forward the worktree at %s to %s: %w", path, tip, err)
	}
	return nil
}

// followOrigin sets the local branch to origin's tip, creating it when it is
// absent. It refuses a local branch with commits origin lacks, which moving
// it would lose.
func (r Repos) followOrigin(clone Clone, branch string) error {
	local, remote := "refs/heads/"+branch, "refs/remotes/origin/"+branch
	if _, err := r.git(clone.Path, "rev-parse", "--verify", "--quiet", local); err == nil {
		if _, err := r.git(clone.Path, "merge-base", "--is-ancestor", local, remote); err != nil {
			return taken("branch %s in %s has commits that origin lacks; push or delete them first", branch, clone.Path)
		}
	}
	if _, err := r.git(clone.Path, "branch", "--force", "--no-track", branch, remote); err != nil {
		return fmt.Errorf("set branch %s to origin's tip: %w", branch, err)
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

// ProbeLimit bounds the run of a repo's `dev` that reads its usage. It is
// generous because a `dev` may build itself, or fetch a toolchain, first.
const ProbeLimit = 3 * time.Minute

// provides reports whether script is a `dev` whose usage lists verb. A repo
// may have its own unrelated `dev`, so being executable is not enough: run
// with no verb, a `dev` prints its usage, which lists the verbs it has. Its
// exit status is ignored, since printing usage usually fails. A `dev` that
// fails or times out before printing a usage that lists the verb counts as
// not providing it.
func (r Repos) provides(dir, script, verb string) bool {
	if !isExecutable(script) {
		return false
	}
	probe := r.Probe
	if probe == nil {
		probe = r.Run
	}
	usage, _ := probe(dir, []string{"GIT_TERMINAL_PROMPT=0"}, script)
	return strings.Contains(usage, verb)
}

// TeardownLimit bounds the repo's own `worktree release`, which is arbitrary
// code, so a hung teardown never holds up a release for long.
const TeardownLimit = 2 * time.Minute

// ReleaseWorktree runs the repo's own `dev worktree release <branch>` in
// engine's clone at clonePath, when the clone's `dev` lists that verb, under
// the Teardown runner. It reports whether the repo provides the verb. The
// repo's contract makes the command idempotent, so it runs whether or not
// the session already tore everything down.
func (r Repos) ReleaseWorktree(clonePath, branch string) (bool, error) {
	script := filepath.Join(clonePath, worktreeCommand)
	if !r.provides(clonePath, script, "worktree release") {
		return false, nil
	}
	unlock, err := lock(clonePath + ".lock")
	if err != nil {
		return true, err
	}
	defer unlock()
	run := r.Teardown
	if run == nil {
		run = r.Run
	}
	if _, err := run(clonePath, []string{"GIT_TERMINAL_PROMPT=0"}, script, "worktree", "release", branch); err != nil {
		return true, fmt.Errorf("the repo's worktree release for %s: %w", branch, err)
	}
	return true, nil
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0
}
