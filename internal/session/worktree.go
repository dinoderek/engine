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

// Worktree is a task's worktree. Created says CreateWorktree made it, and
// NewBranch that it made the branch too, so RemoveWorktree knows what is its
// own to remove. Adopted says it opened on a branch, or in a worktree, an
// earlier attempt left, and Head is then the commit it is at.
type Worktree struct {
	Path      string
	Branch    string
	Created   bool
	NewBranch bool
	Adopted   bool
	Head      string
}

// runMark is the git config entry, branch.<branch>.engineRun, that names the
// run which created a task's branch, so a later attempt of the task knows the
// branch as its own.
const runMark = "engineRun"

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
//
// run is the run the worktree is for, or "" for none. The branch it opens on
// is marked with it (runMark). A branch marked by a run of the same task, which
// an earlier attempt created whether or not it opened a window, is adopted
// as it is, with any worktree on it, even with commits origin lacks or
// uncommitted changes: they are that task's own work. It is fast-forwarded
// to origin's tip only when that loses nothing.
//
// When it made the worktree but a later check fails, it returns the worktree
// with the error, so the caller can remove it.
func (r Repos) CreateWorktree(clone Clone, task int, resume bool, run string) (Worktree, error) {
	if task < 1 {
		return Worktree{}, fmt.Errorf("task number must be positive, got %d", task)
	}
	branch := Branch(clone.Repo, task)
	unlock, err := lock(clone.Path + ".lock")
	if err != nil {
		return Worktree{}, err
	}
	defer unlock()
	own := r.ownBranch(clone, branch, run)
	adopt, existing, err := r.prepareBranch(clone, branch, resume, own)
	if err != nil {
		return Worktree{}, err
	}
	if existing != "" {
		head, err := r.git(existing, "rev-parse", "HEAD")
		if err != nil {
			return Worktree{}, fmt.Errorf("read the head of %s: %w", existing, err)
		}
		return Worktree{Path: existing, Branch: branch, Adopted: true, Head: strings.TrimSpace(head)}, r.mark(clone, branch, run)
	}
	tip := ""
	if own && adopt {
		out, err := r.git(clone.Path, "rev-parse", "refs/heads/"+branch)
		if err != nil {
			return Worktree{}, fmt.Errorf("read the tip of %s: %w", branch, err)
		}
		tip = strings.TrimSpace(out)
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
	wt := Worktree{Path: path, Branch: branch, Created: true, NewBranch: !adopt, Adopted: adopt}
	switch {
	case tip != "":
		wt.Head, err = r.checkAt(path, branch, tip)
	case adopt:
		wt.Head, err = r.checkAtOrigin(clone, path, branch)
	}
	if err != nil {
		return wt, err
	}
	return wt, r.mark(clone, branch, run)
}

// mark records run as the branch's (runMark), so a later attempt of the
// task adopts it. Without a run nothing is written.
func (r Repos) mark(clone Clone, branch, run string) error {
	if run == "" {
		return nil
	}
	if _, err := r.git(clone.Path, "config", "branch."+branch+"."+runMark, run); err != nil {
		return fmt.Errorf("mark branch %s as run %s's: %w", branch, run, err)
	}
	return nil
}

// ownBranch reports whether branch is marked as made by a run of its own
// task, whose runs are named <task name>-<attempt>. Without a run nothing
// is read.
func (r Repos) ownBranch(clone Clone, branch, run string) bool {
	if run == "" {
		return false
	}
	mark, err := r.git(clone.Path, "config", "--get", "branch."+branch+"."+runMark)
	return err == nil && strings.HasPrefix(strings.TrimSpace(mark), strings.TrimPrefix(branch, "task/")+"-")
}

// RemoveWorktree undoes CreateWorktree: when that call made the worktree,
// it runs the repo's own `dev worktree release <branch>` if the repo provides
// it, which tears down what `worktree create` provisioned, then removes any
// worktree still on the branch, and deletes the branch when CreateWorktree
// made that too. A worktree it adopted and a branch it found stay, since they
// hold an earlier attempt's work: a found branch the repo's release deleted
// is put back at its tip.
func (r Repos) RemoveWorktree(clone Clone, wt Worktree) error {
	if !wt.Created {
		return nil
	}
	tip := ""
	if !wt.NewBranch {
		out, _ := r.git(clone.Path, "rev-parse", "--verify", "--quiet", "refs/heads/"+wt.Branch)
		tip = strings.TrimSpace(out)
	}
	if _, err := r.ReleaseWorktree(clone.Path, wt.Branch); err != nil {
		return errors.Join(err, r.restoreBranch(clone, wt.Branch, tip))
	}
	unlock, err := lock(clone.Path + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	left, err := r.worktreeOf(clone, wt.Branch)
	if err != nil {
		return err
	}
	if left != "" {
		if _, err := r.git(clone.Path, "worktree", "remove", "--force", left); err != nil {
			return fmt.Errorf("remove the worktree %s: %w", left, err)
		}
	}
	if !wt.NewBranch {
		return r.restoreBranch(clone, wt.Branch, tip)
	}
	if _, err := r.git(clone.Path, "rev-parse", "--verify", "--quiet", "refs/heads/"+wt.Branch); err != nil {
		return nil // the repo's release deleted it
	}
	if _, err := r.git(clone.Path, "branch", "-D", wt.Branch); err != nil {
		return fmt.Errorf("delete the branch %s: %w", wt.Branch, err)
	}
	return nil
}

// restoreBranch puts back, at tip, a branch the repo's own release deleted
// although this call only adopted it: its commits may exist nowhere else.
// An empty tip, or a branch still there, needs nothing.
func (r Repos) restoreBranch(clone Clone, branch, tip string) error {
	if tip == "" || r.exists(clone.Path, "refs/heads/"+branch) {
		return nil
	}
	if _, err := r.git(clone.Path, "branch", branch, tip); err != nil {
		return fmt.Errorf("restore branch %s at %s, which the repo's worktree release deleted: %w", branch, tip, err)
	}
	return nil
}

// checkAtOrigin fails when a resumed worktree is not at origin's tip of its
// branch, as when a repo's command made the branch anew rather than checking
// it out as it is. It returns the head.
func (r Repos) checkAtOrigin(clone Clone, path, branch string) (string, error) {
	head, err := r.git(path, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("read the head of %s: %w", path, err)
	}
	tip, err := r.git(clone.Path, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+branch)
	if err != nil || strings.TrimSpace(head) != strings.TrimSpace(tip) {
		return "", fmt.Errorf("the worktree at %s is not at origin's tip of %s; the repo's worktree command must check out an existing branch as it is", path, branch)
	}
	return strings.TrimSpace(head), nil
}

// checkAt fails when a worktree opened on a branch an earlier attempt left
// is not at that branch's tip, tip. It returns the head.
func (r Repos) checkAt(path, branch, tip string) (string, error) {
	head, err := r.git(path, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("read the head of %s: %w", path, err)
	}
	if strings.TrimSpace(head) != tip {
		return "", fmt.Errorf("the worktree at %s is not at %s's tip %s, which an earlier attempt left; the repo's worktree command must check out an existing branch as it is", path, branch, tip)
	}
	return tip, nil
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
// worktree already on the branch is adopted, and existing is its path. A
// branch of the task's own (own, CreateWorktree) is adopted on any attempt.
func (r Repos) prepareBranch(clone Clone, branch string, resume, own bool) (adopt bool, existing string, err error) {
	path, err := r.worktreeOf(clone, branch)
	if err != nil {
		return false, "", err
	}
	if path != "" {
		if own {
			return false, path, r.adoptOwnWorktree(clone, branch, path)
		}
		if !resume {
			return false, "", taken("branch %s already has a worktree at %s; release it first", branch, path)
		}
		return false, path, r.adoptWorktree(clone, branch, path)
	}
	if _, err := r.git(clone.Path, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+branch); err == nil {
		if own {
			return true, "", r.followOwn(clone, branch)
		}
		if !resume {
			return false, "", taken("origin already has branch %s, from an earlier run; delete it or continue that work by hand", branch)
		}
		return true, "", r.followOrigin(clone, branch)
	}
	if _, err := r.git(clone.Path, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err != nil {
		return false, "", nil
	}
	if own {
		return true, "", nil
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

// adoptOwnWorktree readies the worktree an earlier attempt of the task left
// on its own branch: as it is, uncommitted changes and commits origin lacks
// included, fast-forwarded to origin's tip of the branch when it is clean
// and behind it. It refuses a worktree that origin's branch has moved on
// from, whether it diverged or has changes a fast-forward would need to
// move, since its commits or origin's would be lost; and, like
// adoptWorktree, engine's clone itself and a worktree whose folder is gone.
func (r Repos) adoptOwnWorktree(clone Clone, branch, path string) error {
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
	tip := "refs/remotes/origin/" + branch
	switch {
	case !r.exists(path, tip) || r.ancestor(path, tip, "HEAD"):
		return nil
	case strings.TrimSpace(status) != "":
		return taken("branch %s has a worktree at %s with uncommitted changes, and origin's branch has commits it lacks; commit, merge and push them first", branch, path)
	case !r.ancestor(path, "HEAD", tip):
		return taken("branch %s at %s and origin's have each moved on; merge them and push first", branch, path)
	}
	if _, err := r.git(path, "merge", "--ff-only", "--quiet", tip); err != nil {
		return fmt.Errorf("fast-forward the worktree at %s to %s: %w", path, tip, err)
	}
	return nil
}

// followOwn readies the task's own local branch, when origin has it too: set
// to origin's tip when it is absent or behind it, and kept as it is when
// ahead, since its commits are the task's own work. A branch that diverged
// from origin's is refused: either side's commits would be lost.
func (r Repos) followOwn(clone Clone, branch string) error {
	local, remote := "refs/heads/"+branch, "refs/remotes/origin/"+branch
	switch {
	case r.exists(clone.Path, local) && r.ancestor(clone.Path, remote, local):
		return nil
	case r.exists(clone.Path, local) && !r.ancestor(clone.Path, local, remote):
		return taken("branch %s in %s and origin's have each moved on; merge them and push first", branch, clone.Path)
	}
	if _, err := r.git(clone.Path, "branch", "--force", "--no-track", branch, remote); err != nil {
		return fmt.Errorf("set branch %s to origin's tip: %w", branch, err)
	}
	return nil
}

// exists reports whether ref names a commit in dir's repository.
func (r Repos) exists(dir, ref string) bool {
	_, err := r.git(dir, "rev-parse", "--verify", "--quiet", ref)
	return err == nil
}

// ancestor reports whether ref is an ancestor of tip, or is tip.
func (r Repos) ancestor(dir, ref, tip string) bool {
	_, err := r.git(dir, "merge-base", "--is-ancestor", ref, tip)
	return err == nil
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
