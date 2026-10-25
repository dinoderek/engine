package hostops

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/session"
)

// released sets up a run of r-7 whose worktree is on disk, with its window
// open when open is set, and releases it.
func released(t *testing.T, open, failed bool, git *gitFake) (Released, string, *tmuxFake, error) {
	t.Helper()
	h := newHome(t, "mac")
	worktree := filepath.Join(h.clone(t)+"-worktrees", "task-r-7")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRun(t, h, session.Record{RunID: "r-7-a", Repo: "o/r", Name: "r-7", Worktree: worktree, Spawned: fixedNow()})
	tm := &tmuxFake{windows: []string{"@1 %1 0 other"}}
	if open {
		tm.windows = append(tm.windows, "@5 %5 1 r-7")
	}
	r, err := Release(Deps{Run: git.run, Tmux: tm.tmux(), Home: h.dir}, "r-7", failed)
	return r, worktree, tm, err
}

func TestReleaseClosesTheWindowAndRemovesTheWorktree(t *testing.T) {
	git := &gitFake{}
	r, worktree, tm, err := released(t, true, false, git)
	if err != nil || r != (Released{Name: "r-7", WindowClosed: true, Worktree: worktree}) {
		t.Errorf("released %+v, %v", r, err)
	}
	if !slices.Contains(tm.calls, "kill-window -t =engine:@5") || slices.Contains(tm.calls, "kill-window -t =engine:@1") {
		t.Errorf("tmux calls %v", tm.calls)
	}
	// Plain git worktree remove: no --force, and the branch stays.
	if want := []string{"git worktree remove " + worktree}; !slices.Equal(git.calls, want) {
		t.Errorf("git calls %v, want %v", git.calls, want)
	}
}

func TestReleaseFailedKeepsTheWorktree(t *testing.T) {
	git := &gitFake{}
	r, worktree, _, err := released(t, true, true, git)
	if err != nil || r != (Released{Name: "r-7", WindowClosed: true, Worktree: worktree, WorktreeKept: true}) || len(git.calls) != 0 {
		t.Errorf("released %+v, %v, git %v", r, err, git.calls)
	}
}

func TestReleaseKeepsAWorktreeGitWillNotRemove(t *testing.T) {
	git := &gitFake{fail: map[string]error{"worktree remove": errors.New("contains modified or untracked files")}}
	r, _, _, err := released(t, false, false, git)
	if err != nil || r.WindowClosed || !r.WorktreeKept || !strings.Contains(r.Note, "modified or untracked") {
		t.Errorf("released %+v, %v", r, err)
	}
}

func TestReleaseWithNothingLeft(t *testing.T) {
	h := newHome(t, "mac")
	writeRun(t, h, session.Record{RunID: "r-7-a", Repo: "o/r", Name: "r-7", Worktree: filepath.Join(h.dir, "gone"), Spawned: fixedNow()})
	git := &gitFake{}
	for _, name := range []string{"r-7", "r-8"} {
		r, err := Release(Deps{Run: git.run, Tmux: (&tmuxFake{}).tmux(), Home: h.dir}, name, false)
		if err != nil || r != (Released{Name: name}) || len(git.calls) != 0 {
			t.Errorf("%s: released %+v, %v, git %v", name, r, err, git.calls)
		}
	}
}

func TestReleaseFailures(t *testing.T) {
	if _, err := Release(Deps{Home: t.TempDir()}, "-x", false); !isRefusal(err) {
		t.Errorf("a bad name: %v", err)
	}
	tm := &tmuxFake{fail: errors.New("tmux died"), windows: []string{}}
	if _, err := Release(Deps{Tmux: tm.tmux(), Home: t.TempDir()}, "r-7", false); err == nil || isRefusal(err) {
		t.Errorf("tmux down: %v", err)
	}
}
