package hostops

import (
	"fmt"
	"strings"

	"github.com/dinoderek/engine/internal/session"
)

// dropBranch deletes rec's local branch from engine's clone once its
// worktree is gone, when everything on it reached the PR: its tip is the
// merged head, or an ancestor of origin's default branch. It reports whether
// it deleted the branch. A branch with commits that never reached the PR is
// kept and named in the error, as is one it could not judge or delete. A
// branch that is already gone, such as one the repo's own release deleted,
// is nothing to do.
func dropBranch(d Deps, rec session.Record, mergedHead string) (bool, error) {
	if rec.Branch == "" {
		return false, nil
	}
	clone, err := cloneOf(d, rec)
	if err != nil {
		return false, err
	}
	ref := "refs/heads/" + rec.Branch
	out, err := d.Run(clone, nil, "git", "rev-parse", "--verify", "--quiet", ref)
	if err != nil {
		return false, nil
	}
	tip := strings.TrimSpace(out)
	if mergedHead == "" || !strings.EqualFold(tip, mergedHead) {
		if err := reachedDefault(d, rec, clone, ref, mergedHead != ""); err != nil {
			return false, fmt.Errorf("kept the branch %s at %s: %w", rec.Branch, tip, err)
		}
	}
	// The merge was a squash or a rebase, which leaves the tip outside the
	// default branch's history, so git's own merged check would refuse.
	if _, err := d.Run(clone, nil, "git", "branch", "-D", rec.Branch); err != nil {
		return false, fmt.Errorf("delete the branch %s: %w", rec.Branch, err)
	}
	return true, nil
}

// reachedDefault fetches origin and fails unless ref is an ancestor of
// origin's default branch. headKnown is whether the merged head was given,
// which decides what the failure says.
func reachedDefault(d Deps, rec session.Record, clone, ref string, headKnown bool) error {
	repo, err := session.ParseRepo(rec.Repo)
	if err != nil {
		return err
	}
	c, err := session.Repos{Run: d.Run, Home: d.Home, Control: rec.Control}.Open(repo)
	if err != nil {
		return err
	}
	if _, err := d.Run(clone, []string{"GIT_TERMINAL_PROMPT=0"}, "git", "fetch", "--quiet", "origin"); err != nil {
		return fmt.Errorf("it could not be judged, as the fetch failed: %w", err)
	}
	if _, err := d.Run(clone, nil, "git", "merge-base", "--is-ancestor", ref, "refs/remotes/origin/"+c.Default); err != nil {
		if !headKnown {
			return fmt.Errorf("the merged head is unknown and it is not in the history of %s, so it cannot be confirmed as merged", c.Default)
		}
		return fmt.Errorf("it has commits that never reached %s and are not the merged head", c.Default)
	}
	return nil
}
