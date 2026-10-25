package hostops

import (
	"errors"
	"io/fs"
	"os"
	"strings"

	"github.com/dinoderek/engine/internal/session"
)

// Released is release's reply.
type Released struct {
	Name         string `json:"name"`
	WindowClosed bool   `json:"window_closed"`
	// Worktree is the newest run's worktree, when it still was on disk.
	Worktree     string `json:"worktree,omitempty"`
	WorktreeKept bool   `json:"worktree_kept"`
	// Note says what went wrong without failing the release: why a worktree
	// was kept when the run had not failed, or a run.json it could not read.
	Note string `json:"note,omitempty"`
}

// Release closes the session's window if it is still open, and removes the
// worktree of its newest run, unless the run failed: a failed run's worktree
// is kept for the operator. Removal is git's plain `worktree remove`, which
// keeps a worktree with uncommitted changes, and the branch always stays.
func Release(d Deps, name string, failed bool) (Released, error) {
	if !nameRe.MatchString(name) {
		return Released{}, refuse("session name %q: use letters, digits, _, . and -, not starting with -", name)
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
	// An unreadable run.json of another run matters only when this
	// session's own run is unknown.
	run, ok, err := session.LatestRun(d.Home, name)
	if !ok || run.Record.Worktree == "" {
		return r, err
	}
	if err != nil {
		r.Note = err.Error()
	}
	if _, err := os.Stat(run.Record.Worktree); errors.Is(err, fs.ErrNotExist) {
		return r, nil
	}
	r.Worktree, r.WorktreeKept = run.Record.Worktree, true
	if failed {
		return r, nil
	}
	if err := removeWorktree(d, run.Record); err != nil {
		r.Note = strings.TrimPrefix(r.Note+"; "+err.Error(), "; ")
		return r, nil
	}
	r.WorktreeKept = false
	return r, nil
}

// removeWorktree removes rec's worktree from engine's clone of its repo.
func removeWorktree(d Deps, rec session.Record) error {
	repo, err := session.ParseRepo(rec.Repo)
	if err != nil {
		return err
	}
	clone, err := session.Repos{Home: d.Home}.Path(repo)
	if err != nil {
		return err
	}
	_, err = d.Run(clone, nil, "git", "worktree", "remove", rec.Worktree)
	return err
}
