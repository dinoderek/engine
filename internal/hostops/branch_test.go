package hostops

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/session"
)

// mergedRun is a done run of o/r's task r-7 on task/r-7, in real git: its
// worktree has one commit of its own on top of main, pushed to origin.
type mergedRun struct {
	d        Deps
	clone    string
	worktree string
	home     home
}

func newMergedRun(t *testing.T) mergedRun {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	h := newHome(t, "mac")
	d := Deps{Run: realGit, Tmux: (&tmuxFake{}).tmux(), Home: h.dir, BaseURL: "file://" + bareRemote(t)}
	synced, err := SyncRepo(d, "o/r")
	if err != nil {
		t.Fatal(err)
	}
	m := mergedRun{d: d, clone: synced.Clone, home: h, worktree: filepath.Join(synced.Clone+"-worktrees", "task-r-7")}
	mustGit(t, synced.Clone, "worktree", "add", "--quiet", "-b", "task/r-7", m.worktree, "origin/main")
	m.commit(t, "work")
	mustGit(t, m.worktree, "push", "--quiet", "origin", "task/r-7")
	writeRun(t, h, session.Record{RunID: "r-7-a", Repo: "o/r", Name: "r-7", Branch: "task/r-7", Worktree: m.worktree, Spawned: fixedNow()})
	return m
}

// commit adds a commit with its own file to the worktree's branch.
func (m mergedRun) commit(t *testing.T, name string) {
	t.Helper()
	write(t, filepath.Join(m.worktree, name+".txt"), name+"\n", 0o644)
	mustGit(t, m.worktree, "add", name+".txt")
	mustGit(t, m.worktree, "commit", "--quiet", "-m", name)
}

// tip is the worktree branch's head commit.
func (m mergedRun) tip(t *testing.T) string {
	t.Helper()
	out, err := realGit(m.worktree, nil, "git", "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out)
}

// branchExists reports whether engine's clone still has the task branch.
func (m mergedRun) branchExists() bool {
	_, err := realGit(m.clone, nil, "git", "rev-parse", "--verify", "--quiet", "refs/heads/task/r-7")
	return err == nil
}

func TestReleaseDeletesABranchWhoseTipIsTheMergedHead(t *testing.T) {
	m := newMergedRun(t)
	// A squash merge leaves the tip outside main's history: only the merged
	// head says it reached the PR.
	r, err := Release(m.d, "r-7", RunDone, m.tip(t))
	if err != nil || !r.BranchDeleted || r.WorktreeKept || r.Note != "" || m.branchExists() {
		t.Errorf("released %+v, %v; branch exists %v", r, err, m.branchExists())
	}
}

func TestReleaseDeletesABranchMergedIntoTheDefaultBranch(t *testing.T) {
	m := newMergedRun(t)
	mustGit(t, m.worktree, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	// The head is unknown, and the clone has not fetched the merge yet.
	r, err := Release(m.d, "r-7", RunDone, "")
	if err != nil || !r.BranchDeleted || r.Note != "" || m.branchExists() {
		t.Errorf("released %+v, %v; branch exists %v", r, err, m.branchExists())
	}
}

func TestReleaseKeepsABranchWithCommitsThatNeverReachedThePR(t *testing.T) {
	m := newMergedRun(t)
	merged := m.tip(t)
	m.commit(t, "after")
	r, err := Release(m.d, "r-7", RunDone, merged)
	if err != nil || r.BranchDeleted || r.WorktreeKept || !m.branchExists() ||
		!strings.Contains(r.Note, "kept the branch task/r-7") || !strings.Contains(r.Note, "never reached main") {
		t.Errorf("released %+v, %v; branch exists %v", r, err, m.branchExists())
	}
}

func TestReleaseKeepsTheBranchOfAWorktreeItKept(t *testing.T) {
	m := newMergedRun(t)
	write(t, filepath.Join(m.worktree, "dirty.txt"), "x\n", 0o644)
	r, err := Release(m.d, "r-7", RunDone, m.tip(t))
	if err != nil || !r.WorktreeKept || r.BranchDeleted || !m.branchExists() {
		t.Errorf("released %+v, %v; branch exists %v", r, err, m.branchExists())
	}
}

func TestOnlyADoneRunsReleaseDeletesTheBranch(t *testing.T) {
	for _, outcome := range []Outcome{RunEnded, RunFailed} {
		m := newMergedRun(t)
		r, err := Release(m.d, "r-7", outcome, m.tip(t))
		if err != nil || r.BranchDeleted || !m.branchExists() {
			t.Errorf("%q: released %+v, %v; branch exists %v", outcome, r, err, m.branchExists())
		}
	}
}

// branchGone is a done run whose worktree is already gone, as when the
// repo's own release removed it, in a clone that git fakes.
func branchGone(t *testing.T, git *gitFake, head string) (Released, error) {
	t.Helper()
	h := newHome(t, "mac")
	h.clone(t)
	writeRun(t, h, session.Record{RunID: "r-7-a", Repo: "o/r", Name: "r-7", Branch: "task/r-7", Worktree: filepath.Join(h.dir, "gone"), Spawned: fixedNow()})
	return Release(Deps{Run: git.run, Tmux: (&tmuxFake{}).tmux(), Home: h.dir}, "r-7", RunDone, head)
}

func TestAFailedBranchDeleteIsANoteNotAFailure(t *testing.T) {
	git := &gitFake{made: true, fail: map[string]error{"branch -D": errors.New("cannot lock ref")}}
	r, err := branchGone(t, git, "abc")
	if err != nil || r.BranchDeleted || !strings.Contains(r.Note, "delete the branch task/r-7") || !strings.Contains(r.Note, "cannot lock ref") {
		t.Errorf("released %+v, %v", r, err)
	}
}

func TestABranchThatCannotBeJudgedIsKept(t *testing.T) {
	git := &gitFake{made: true, fail: map[string]error{"fetch": errors.New("network is down")}}
	r, err := branchGone(t, git, "other")
	if err != nil || r.BranchDeleted || !strings.Contains(r.Note, "could not be judged") || !strings.Contains(r.Note, "network is down") {
		t.Errorf("released %+v, %v", r, err)
	}
	for _, call := range git.calls {
		if strings.Contains(call, "branch -D") {
			t.Errorf("deleted a branch it could not judge: %v", git.calls)
		}
	}
}

func TestAMissingBranchIsNothingToDelete(t *testing.T) {
	git := &gitFake{}
	r, err := branchGone(t, git, "abc")
	if err != nil || r.BranchDeleted || r.Note != "" {
		t.Errorf("released %+v, %v", r, err)
	}
}

func TestABranchWithNoKnownMergedHeadIsKeptWithoutBlamingItsCommits(t *testing.T) {
	m := newMergedRun(t)
	r, err := Release(m.d, "r-7", RunDone, "")
	if err != nil || r.BranchDeleted || !m.branchExists() || !strings.Contains(r.Note, "merged head is unknown") || strings.Contains(r.Note, "never reached") {
		t.Errorf("released %+v, %v; branch exists %v", r, err, m.branchExists())
	}
}
