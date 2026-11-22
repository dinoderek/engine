package session

import (
	"fmt"
	"strings"
)

// keptBranch is what restoreBranch puts back: a branch's tip and its run
// mark, which deleting the branch deletes too.
type keptBranch struct{ tip, run string }

// keep reads branch's tip and mark.
func (r Repos) keep(clone Clone, branch string) keptBranch {
	tip, _ := r.git(clone.Path, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	run, _ := r.git(clone.Path, "config", "--get", "branch."+branch+"."+runMark)
	return keptBranch{tip: strings.TrimSpace(tip), run: strings.TrimSpace(run)}
}

// restoreBranch puts back, at its tip and with its mark, a branch the repo's
// own release deleted although this call only adopted it: its commits may
// exist nowhere else, and the mark keeps it the task's own. A branch with no
// tip, or one still there, needs nothing.
func (r Repos) restoreBranch(clone Clone, branch string, kept keptBranch) error {
	if kept.tip == "" || r.exists(clone.Path, "refs/heads/"+branch) {
		return nil
	}
	if _, err := r.git(clone.Path, "branch", branch, kept.tip); err != nil {
		return fmt.Errorf("restore branch %s at %s, which the repo's worktree release deleted: %w", branch, kept.tip, err)
	}
	return r.mark(clone, branch, kept.run)
}
