package hostops

import (
	"errors"
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
	// Note says what went wrong without failing the release: the repo's
	// `worktree release` failing or timing out, why a worktree was kept when
	// the run had not failed, a run.json it could not read, or a harness's
	// files it could not remove.
	Note string `json:"note,omitempty"`
}

// Release closes the session's window if it is still open, deletes what each
// harness adapter wrote for this session alone, and removes the worktree of
// its newest run, unless the run failed: a failed run's worktree is kept for
// the operator. For a done task's run, the repo's own `dev worktree release
// <branch>` runs first, from engine's clone, when the repo provides it, best
// effort under TeardownLimit. Engine's removal is git's plain `worktree
// remove` of a worktree still on disk, which keeps one with uncommitted
// changes; engine never deletes the branch.
func Release(d Deps, name string, outcome Outcome) (Released, error) {
	if !nameRe.MatchString(name) {
		return Released{}, refuse("session name %q: use letters, digits, _, . and -, not starting with -", name)
	}
	adapters, err := d.adapters()
	if err != nil {
		return Released{}, err
	}
	r := Released{Name: name}
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
	if _, err := os.Stat(run.Record.Worktree); errors.Is(err, fs.ErrNotExist) {
		return r, nil
	}
	r.Worktree, r.WorktreeKept = run.Record.Worktree, true
	if outcome == RunFailed {
		return r, nil
	}
	if err := removeWorktree(d, run.Record); err != nil {
		r.note(err)
		return r, nil
	}
	r.WorktreeKept = false
	return r, nil
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
