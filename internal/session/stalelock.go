package session

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// clearStaleLock removes the index.lock of the worktree that has branch
// checked out, when adopting is set and no running process holds it open. A
// reboot can leave the lock behind, and a resume would fail on it. It reports
// whether it removed one. A lock a process holds is kept, and the adoption is
// refused, since that process may be writing the worktree now.
func (r Repos) clearStaleLock(clone Clone, branch string, adopting bool) (bool, error) {
	if !adopting {
		return false, nil
	}
	path, err := r.worktreeOf(clone, branch)
	if err != nil || path == "" {
		return false, err
	}
	out, err := r.git(path, "rev-parse", "--git-path", "index.lock")
	if err != nil {
		return false, fmt.Errorf("find the index lock of the worktree at %s: %w", path, err)
	}
	lock := strings.TrimSpace(out)
	if lock == "" {
		return false, nil
	}
	if !filepath.IsAbs(lock) {
		lock = filepath.Join(path, lock)
	}
	if _, err := os.Stat(lock); errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	held, err := heldOpen(lock, path)
	if err != nil {
		return false, err
	}
	if held {
		return false, taken("branch %s's worktree at %s has %s held by a running process; wait for it, or stop that process, first", branch, path, lock)
	}
	// prepareBranch may still refuse the adoption after this, leaving the
	// lock removed: it is stale here, so that does no harm.
	if err := os.Remove(lock); err != nil {
		return false, fmt.Errorf("remove the stale index lock %s: %w", lock, err)
	}
	return true, nil
}

// heldOpen reports whether the lock is held: some process has file open, or a
// git process works in worktree. Git keeps index.lock while an editor runs
// inside commit, with the file closed, so the second check is needed too.
// lsof exits 1 when it finds nothing (or the file is gone), and any other
// failure is an error, so a lock is never removed on a guess.
func heldOpen(file, worktree string) (bool, error) {
	open, err := lsof("-t", "--", file)
	if err != nil || len(open) > 0 {
		return len(open) > 0, err
	}
	dir := worktree
	if resolved, err := filepath.EvalSymlinks(worktree); err == nil {
		dir = resolved
	}
	cwds, err := lsof("-a", "-d", "cwd", "-c", "git", "-Fn")
	if err != nil {
		return false, err
	}
	for _, c := range cwds {
		if p, ok := strings.CutPrefix(c, "n"); ok && (p == dir || strings.HasPrefix(p, dir+string(filepath.Separator))) {
			return true, nil
		}
	}
	return false, nil
}

// lsof runs lsof with args and returns its non-empty output lines. Lines, not
// fields: a path may hold a space.
func lsof(args ...string) ([]string, error) {
	out, err := exec.Command("lsof", args...).Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return nil, nil
		}
		return nil, fmt.Errorf("check for a process holding a file with lsof: %w", err)
	}
	var lines []string
	for _, line := range strings.Split(string(out), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines, nil
}
