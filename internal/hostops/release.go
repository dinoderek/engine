package hostops

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/session"
)

// Outcome is how the released session's run ended, which decides what
// release removes.
type Outcome string

// The outcomes a caller releases a session for.
const (
	// RunEnded is a run whose task goes on, such as an ended attempt before
	// a resume: only engine's own worktree removal runs.
	RunEnded Outcome = ""
	// RunFailed keeps the worktree for the operator.
	RunFailed Outcome = "failed"
	// RunStopped is a run the operator stopped: its task is held, and the
	// worktree stays for the resume.
	RunStopped Outcome = "stopped"
	// RunMoved is a run the operator moved to another host or harness: its
	// branch is pushed to origin before the window closes, and the worktree
	// and branch stay for a resume on this host.
	RunMoved Outcome = "moved"
	// RunDone is a done task's run: the repo's own `worktree release` runs
	// first.
	RunDone Outcome = "done"
)

// Released is release's reply.
type Released struct {
	Name         string `json:"name"`
	WindowClosed bool   `json:"window_closed"`
	// RepoReleased is true when the repo's own `worktree release` ran.
	RepoReleased bool `json:"repo_released,omitempty"`
	// Worktree is the newest run's worktree, when it still was on disk.
	Worktree     string `json:"worktree,omitempty"`
	WorktreeKept bool   `json:"worktree_kept"`
	// BranchDeleted is true when a done task's local branch was deleted.
	BranchDeleted bool `json:"branch_deleted,omitempty"`
	// Pushed is true when a moved run's branch was pushed to origin;
	// Unpushed says what a moved run left that origin lacks: commits a push
	// failed to send, or uncommitted changes in the worktree.
	Pushed   bool   `json:"pushed,omitempty"`
	Unpushed string `json:"unpushed,omitempty"`
	// Note says what went wrong without failing the release: the repo's
	// `worktree release` failing or timing out, why a worktree was kept when
	// the run had not failed, why a done task's branch was kept or not
	// deleted, a run.json it could not read, or a harness's files it could
	// not remove.
	Note string `json:"note,omitempty"`
}

// Release closes the session's window if it is still open, deletes what each
// harness adapter wrote for this session alone, and removes the worktree of
// its newest run, unless the run failed, when it is kept for the operator, or
// was stopped or moved, when it is kept for the resume. A moved run's branch
// is pushed to origin first, while the window is still open. For a done task's run, the
// repo's own `dev worktree release <branch>` runs first, from engine's clone,
// when the repo provides it, best effort under TeardownLimit. Engine's
// removal is git's plain `worktree remove` of a worktree still on disk, which
// keeps one with uncommitted changes. For a done task's run, once the
// worktree is gone, engine deletes the local branch when its tip is
// mergedHead or an ancestor of origin's default branch, and otherwise keeps
// it and says so in the note. Any other release never deletes the branch.
func Release(d Deps, name string, outcome Outcome, mergedHead string) (Released, error) {
	if !nameRe.MatchString(name) {
		return Released{}, refuse("session name %q: use letters, digits, _, . and -, not starting with -", name)
	}
	adapters, err := d.adapters()
	if err != nil {
		return Released{}, err
	}
	r := Released{Name: name}
	if outcome == RunMoved {
		r.push(d, name)
	}
	windows, err := d.Tmux.Windows()
	if err != nil {
		return Released{}, err
	}
	for _, w := range windows {
		if w.Name != name {
			continue
		}
		if err := d.Tmux.Kill(w.ID); err != nil {
			return r, err
		}
		r.WindowClosed = true
	}
	run, ok, err := session.LatestRun(d.Home, name)
	// Every adapter releases the session, whatever the run's harness: a
	// spawn that failed before run.json was written may have left files.
	r.note(releaseAll(adapters, name, run.Record.Worktree))
	// An unreadable run.json fails the release only when no run of this
	// session could be read; otherwise the newest readable one is released,
	// and the note says what was skipped.
	if !ok || run.Record.Worktree == "" {
		return r, err
	}
	if err != nil {
		r.note(err)
	}
	if outcome == RunDone {
		ran, err := repoRelease(d, run.Record)
		r.RepoReleased = ran
		r.note(err)
	}
	r.releaseWorktree(d, run.Record, outcome)
	if outcome == RunDone && !r.WorktreeKept {
		deleted, err := dropBranch(d, run.Record, mergedHead)
		r.BranchDeleted = deleted
		r.note(err)
	}
	return r, nil
}

// releaseWorktree removes rec's worktree unless it is gone already or the
// run failed or was stopped, and records what it found in r.
func (r *Released) releaseWorktree(d Deps, rec session.Record, outcome Outcome) {
	if _, err := os.Stat(rec.Worktree); errors.Is(err, fs.ErrNotExist) {
		return
	}
	r.Worktree, r.WorktreeKept = rec.Worktree, true
	if outcome == RunFailed || outcome == RunStopped || outcome == RunMoved {
		return
	}
	if err := removeWorktree(d, rec); err != nil {
		r.note(err)
		return
	}
	r.WorktreeKept = false
}

// push pushes the branch of the session's newest run to origin, without
// force, so a resume elsewhere starts from its commits, and records in r
// what origin still lacks: the commits a failed push left, and uncommitted
// changes in the worktree.
func (r *Released) push(d Deps, name string) {
	run, ok, _ := session.LatestRun(d.Home, name)
	rec := run.Record
	if !ok || rec.Branch == "" {
		r.Unpushed = "no run of " + name + " records its branch, so nothing was pushed"
		return
	}
	var left []string
	clone, err := cloneOf(d, rec)
	if err == nil {
		ref := "refs/heads/" + rec.Branch
		_, err = d.Run(clone, []string{"GIT_TERMINAL_PROMPT=0"}, "git", "push", "--quiet", "origin", ref+":"+ref)
	}
	if err != nil {
		left = append(left, fmt.Sprintf("the commits on %s that origin lacks, as the push failed: %v", rec.Branch, err))
	}
	r.Pushed = err == nil
	if _, err := os.Stat(rec.Worktree); rec.Worktree != "" && err == nil {
		status, err := d.Run(rec.Worktree, nil, "git", "status", "--porcelain")
		if err != nil || strings.TrimSpace(status) != "" {
			left = append(left, "uncommitted changes in "+rec.Worktree)
		}
	}
	r.Unpushed = strings.Join(left, "; ")
}

// note adds err, if any, to the reply's note.
func (r *Released) note(err error) {
	if err != nil {
		r.Note = strings.TrimPrefix(r.Note+"; "+err.Error(), "; ")
	}
}

// cloneOf is the path of engine's clone that holds rec's worktree.
func cloneOf(d Deps, rec session.Record) (string, error) {
	repo, err := session.ParseRepo(rec.Repo)
	if err != nil {
		return "", err
	}
	return session.Repos{Home: d.Home, Control: rec.Control}.Path(repo)
}

// repoRelease runs the repo's own `worktree release` for rec's branch from
// engine's clone, and reports whether the repo provides it.
func repoRelease(d Deps, rec session.Record) (bool, error) {
	if rec.Branch == "" {
		return false, nil
	}
	clone, err := cloneOf(d, rec)
	if err != nil {
		return false, err
	}
	repos := session.Repos{Run: d.Run, Probe: d.Probe, Teardown: d.Teardown}
	return repos.ReleaseWorktree(clone, rec.Branch)
}

// removeWorktree removes rec's worktree from engine's clone of its repo.
func removeWorktree(d Deps, rec session.Record) error {
	clone, err := cloneOf(d, rec)
	if err != nil {
		return err
	}
	_, err = d.Run(clone, nil, "git", "worktree", "remove", rec.Worktree)
	return err
}

// releaseAll has every adapter release the session called name, whose
// newest run's worktree is dir.
func releaseAll(a harness.Adapters, name, dir string) error {
	var errs []error
	for _, n := range a.Names() {
		h, _ := a.Get(n)
		errs = append(errs, h.Session(name).Release(dir))
	}
	return errors.Join(errs...)
}
