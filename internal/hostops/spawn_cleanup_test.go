package hostops

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/harness/harnesstest"
	"github.com/dinoderek/engine/internal/session"
)

func ran(git *gitFake, part string) bool {
	return slices.ContainsFunc(git.calls, func(c string) bool { return strings.Contains(c, part) })
}

// A spawn whose window fails to open removes its run directory, and the
// worktree and branch it made, so the next dispatch of the run starts clean.
func TestSpawnThatFailsRemovesWhatItCreated(t *testing.T) {
	h := newHome(t, "mac")
	clone := h.clone(t)
	git := &gitFake{}
	tm := &tmuxFake{openErr: errors.New("tmux died")}
	req := request()
	runDir := filepath.Join(h.dir, session.RunsDir, req.RunID)
	tm.opening = func() {
		if _, err := os.Stat(runDir); err != nil {
			t.Errorf("the run directory was not made before the window: %v", err)
		}
	}
	fake := &harnesstest.Fake{Called: Claude}
	d := spawnDeps(t, h, git.run, tm, nil)
	d.Harnesses = harness.NewAdapters(fake)
	_, err := Spawn(d, req)
	if err == nil || !strings.Contains(err.Error(), "tmux died") || strings.Contains(err.Error(), "left in place") {
		t.Fatalf("err = %v, want the tmux failure with nothing left", err)
	}
	if _, err := os.Stat(runDir); !os.IsNotExist(err) {
		t.Errorf("the run directory is still there: %v", err)
	}
	worktree := clone + "-worktrees/task-r-7"
	if _, err := os.Stat(worktree); !os.IsNotExist(err) || !ran(git, "worktree remove --force "+worktree) || !ran(git, "branch -D task/r-7") {
		t.Errorf("the new worktree and branch were not removed: %v", git.calls)
	}
	if calls := fake.Calls(); !slices.Contains(calls, "release r-7 "+worktree) {
		t.Errorf("the harness's trust was not released: %v", calls)
	}
	// The next dispatch of the same run starts clean.
	if _, err := Spawn(spawnDeps(t, h, (&gitFake{}).run, &tmuxFake{}, nil), req); err != nil {
		t.Errorf("the next dispatch: %v", err)
	}
}

// A resume keeps what it did not make: the branch origin had, and a worktree
// an earlier attempt left, which it adopted.
func TestSpawnThatFailsKeepsWhatItReused(t *testing.T) {
	h := newHome(t, "mac")
	clone := h.clone(t)
	req := request()
	req.Resume = true

	fromOrigin := &gitFake{origin: true}
	if _, err := Spawn(spawnDeps(t, h, fromOrigin.run, &tmuxFake{openErr: errors.New("tmux died")}, nil), req); err == nil {
		t.Fatal("the window opened")
	}
	if !ran(fromOrigin, "worktree remove --force") || ran(fromOrigin, "branch -D") {
		t.Errorf("a resume from origin's branch: removed %v, want its worktree only", fromOrigin.calls)
	}

	left := clone + "-worktrees/task-r-7"
	if err := os.MkdirAll(left, 0o755); err != nil {
		t.Fatal(err)
	}
	adopted := &gitFake{origin: true, ancestor: true, added: left}
	if _, err := Spawn(spawnDeps(t, h, adopted.run, &tmuxFake{openErr: errors.New("tmux died")}, nil), req); err == nil {
		t.Fatal("the window opened")
	}
	if ran(adopted, "worktree add") || ran(adopted, "worktree remove") || ran(adopted, "branch -D") {
		t.Errorf("an adopted worktree was touched: %v", adopted.calls)
	}
	if _, err := os.Stat(left); err != nil {
		t.Errorf("the adopted worktree is gone: %v", err)
	}
}

// When removal itself fails, the error names each leftover with the command
// that removes it.
func TestSpawnCleanupNamesWhatItCouldNotRemove(t *testing.T) {
	h := newHome(t, "mac")
	clone := h.clone(t)
	git := &gitFake{fail: map[string]error{"worktree remove": errors.New("worktree locked")}}
	_, err := Spawn(spawnDeps(t, h, git.run, &tmuxFake{openErr: errors.New("tmux died")}, nil), request())
	worktree := clone + "-worktrees/task-r-7"
	for _, want := range []string{"tmux died", "left in place", "the worktree " + worktree + " and branch task/r-7 (remove the worktree",
		"worktree locked", "worktree remove --force " + worktree + " && git -C " + clone + " branch -D task/r-7"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want %q", err, want)
		}
	}

	runDir := filepath.Join(h.dir, session.RunsDir, "r")
	u := &undo{runDir: runDir, removeAll: func(string) error { return errors.New("busy") }}
	err = u.run(errors.New("tmux died"))
	if want := "left in place, to remove before starting again: the run directory " + runDir + " (busy); remove it with rm -rf " + runDir; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("err = %v, want %q", err, want)
	}
	if err := (&undo{opened: true}).run(errors.New("run.json")); err.Error() != "run.json" {
		t.Errorf("an open window undid: %v", err)
	}
}

// A run directory a dead dispatch left, with no run.json and no window of
// its name, is cleared and reused.
func TestSpawnReusesALeftoverRunDirectory(t *testing.T) {
	h := newHome(t, "mac")
	h.clone(t)
	req := request()
	stale := filepath.Join(h.dir, session.RunsDir, req.RunID, "server-token")
	write(t, stale, "old\n", 0o600)
	got, err := Spawn(spawnDeps(t, h, (&gitFake{}).run, &tmuxFake{windows: []string{"@1 %1 0 other"}}, nil), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("the leftover file survived: %v", err)
	}
	if _, err := session.ReadRecord(got.RunDir); err != nil {
		t.Errorf("run.json: %v", err)
	}
}
