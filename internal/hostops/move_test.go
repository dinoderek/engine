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

// movedRelease releases r-7, whose run is on branch task/r-7 with its
// worktree on disk and its window open, as moved. run answers git; tm
// records tmux.
func movedRelease(t *testing.T, run session.RunFunc, tm *tmuxFake) (Released, string, error) {
	t.Helper()
	h := newHome(t, "mac")
	worktree := filepath.Join(h.clone(t)+"-worktrees", "task-r-7")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRun(t, h, session.Record{RunID: "r-7-a", Repo: "o/r", Name: "r-7", Branch: "task/r-7", Worktree: worktree, Spawned: fixedNow()})
	r, err := Release(Deps{Run: run, Tmux: tm.tmux(), Home: h.dir}, "r-7", RunMoved, "")
	return r, worktree, err
}

// A moved run's branch is pushed, without force, while its window is still
// open; then the window closes, and the worktree and branch stay.
func TestReleaseMovedPushesBeforeClosingAndKeepsTheWorktree(t *testing.T) {
	tm := &tmuxFake{windows: []string{"@5 %5 1 r-7"}}
	var calls []string
	closedAtPush := true
	run := func(dir string, env []string, name string, args ...string) (string, error) {
		line := name + " " + strings.Join(args, " ")
		calls = append(calls, line)
		if strings.HasPrefix(line, "git push") {
			closedAtPush = slices.Contains(tm.calls, "kill-window -t =engine:@5")
			if !slices.Contains(env, "GIT_TERMINAL_PROMPT=0") || !strings.HasSuffix(dir, "repos/o/r") {
				t.Errorf("push ran in %s with %v", dir, env)
			}
		}
		return "", nil
	}
	r, worktree, err := movedRelease(t, run, tm)
	if err != nil || r != (Released{Name: "r-7", WindowClosed: true, Worktree: worktree, WorktreeKept: true, Pushed: true}) {
		t.Fatalf("released %+v, %v", r, err)
	}
	if closedAtPush {
		t.Error("the window closed before the push")
	}
	want := []string{"git push --quiet origin refs/heads/task/r-7:refs/heads/task/r-7", "git status --porcelain"}
	if !slices.Equal(calls, want) {
		t.Errorf("git calls %v, want %v", calls, want)
	}
	if _, err := os.Stat(worktree); err != nil {
		t.Errorf("the worktree is gone: %v", err)
	}
}

// What the push could not send, and what was never committed, is said; the
// release still closes the window.
func TestReleaseMovedSaysWhatOriginLacks(t *testing.T) {
	run := func(_ string, _ []string, name string, args ...string) (string, error) {
		switch args[0] {
		case "push":
			return "", errors.New("rejected: non-fast-forward")
		case "status":
			return " M main.go\n", nil
		}
		return "", nil
	}
	tm := &tmuxFake{windows: []string{"@5 %5 1 r-7"}}
	r, worktree, err := movedRelease(t, run, tm)
	if err != nil || !r.WindowClosed || !r.WorktreeKept || r.Pushed {
		t.Fatalf("released %+v, %v", r, err)
	}
	for _, want := range []string{"task/r-7", "non-fast-forward", "uncommitted changes in " + worktree} {
		if !strings.Contains(r.Unpushed, want) {
			t.Errorf("unpushed %q lacks %q", r.Unpushed, want)
		}
	}
}

func TestReleaseMovedWithNoBranchPushesNothing(t *testing.T) {
	h := newHome(t, "mac")
	writeRun(t, h, session.Record{RunID: "r-7-a", Repo: "o/r", Name: "r-7", Worktree: filepath.Join(h.dir, "gone"), Spawned: fixedNow()})
	r, err := Release(Deps{Run: noGit(t), Tmux: (&tmuxFake{}).tmux(), Home: h.dir}, "r-7", RunMoved, "")
	if err != nil || r.Pushed || !strings.Contains(r.Unpushed, "nothing was pushed") {
		t.Errorf("released %+v, %v", r, err)
	}
}

// The release op takes --moved, which excludes the other outcomes.
func TestReleaseMovedIsAFlagOfItsOwn(t *testing.T) {
	h := newHome(t, "mac")
	writeRun(t, h, session.Record{RunID: "r-7-a", Repo: "o/r", Name: "r-7", Branch: "task/r-7", Worktree: filepath.Join(h.dir, "gone"), Spawned: fixedNow()})
	git := &gitFake{}
	d := Deps{Tmux: (&tmuxFake{}).tmux(), Home: h.dir, Exec: agentsFake("[]", nil), Run: git.run}
	code, reply, _ := call(t, d, "", "release", "--json", "--name", "r-7", "--moved")
	if code != 0 || reply["pushed"] != true || len(git.calls) != 1 || !strings.HasPrefix(git.calls[0], "git push") {
		t.Errorf("release --moved: exit %d, reply %v, git %v", code, reply, git.calls)
	}
	for _, other := range []string{"--failed", "--stopped", "--done"} {
		code, reply, _ = call(t, d, "", "release", "--json", "--name", "r-7", "--moved", other)
		if msg, _ := reply["error"].(string); code != 1 || reply["refused"] != true || !strings.Contains(msg, "exclusive") {
			t.Errorf("--moved %s: exit %d, reply %v", other, code, reply)
		}
	}
}
