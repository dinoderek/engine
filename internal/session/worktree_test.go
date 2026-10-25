package session

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestTaskNameAndBranch(t *testing.T) {
	repo := mustRepo(t, "dinoderek/engine")
	if got := TaskName(repo, 412); got != "engine-412" {
		t.Errorf("TaskName = %q", got)
	}
	if got := Branch(repo, 412); got != "task/engine-412" {
		t.Errorf("Branch = %q", got)
	}
	if strings.Contains(TaskName(repo, 412), "#") {
		t.Error("a session name never holds #: tmux treats it as special")
	}
}

// porcelain is what `git worktree list --porcelain` prints for a clone with
// one task worktree.
func porcelain(clone, wt, branch string) string {
	return "worktree " + clone + "\nHEAD abc\ndetached\n\nworktree " + wt + "\nHEAD def\nbranch refs/heads/" + branch + "\n"
}

// worktreeFixture is a clone in a temp dir, and a fake that, once the
// worktree command ran, lists the task's worktree.
func worktreeFixture(t *testing.T, withDev bool, mode os.FileMode) (Repos, Clone, *gitFake, string) {
	t.Helper()
	repo := mustRepo(t, "o/r")
	f := &gitFake{}
	r := newRepos(t, f)
	path, _ := r.Path(repo)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if withDev {
		if err := os.WriteFile(filepath.Join(path, "dev"), []byte("#!/bin/sh\n"), mode); err != nil {
			t.Fatal(err)
		}
	}
	wt := path + "-worktrees/task-r-7"
	f.replies = []reply{
		{key: "rev-parse --verify", err: errBoom},
		{key: "worktree list", out: "worktree " + path + "\nHEAD abc\ndetached\n"},
	}
	f.onRun = func(c call) {
		if strings.Contains(c.line, "worktree create") || strings.Contains(c.line, "worktree add") {
			f.replies[1].out = porcelain(path, wt, "task/r-7")
		}
	}
	return r, Clone{Repo: repo, Path: path, Default: "main"}, f, wt
}

func TestCreateWorktreeWithTheReposOwnCommand(t *testing.T) {
	r, clone, f, wt := worktreeFixture(t, true, 0o755)
	got, err := r.CreateWorktree(clone, 7)
	if err != nil {
		t.Fatal(err)
	}
	if got != (Worktree{Path: wt, Branch: "task/r-7"}) {
		t.Errorf("worktree = %+v", got)
	}
	script := filepath.Join(clone.Path, "dev")
	var ran []call
	for _, c := range f.calls {
		if strings.Contains(c.line, "worktree create") {
			ran = append(ran, c)
		}
	}
	if len(ran) != 1 || ran[0].line != script+" worktree create task/r-7" || ran[0].dir != clone.Path {
		t.Errorf("the repo's command ran as %+v", ran)
	}
	if !reflect.DeepEqual(ran[0].env, []string{"GH_TOKEN=tok", "GIT_TERMINAL_PROMPT=0"}) {
		t.Errorf("env = %v", ran[0].env)
	}
	if f.has("worktree add") {
		t.Error("git worktree add ran besides the repo's command")
	}
}

func TestCreateWorktreeFallsBackToGit(t *testing.T) {
	tests := []struct {
		name    string
		withDev bool
		mode    os.FileMode
	}{
		{"no dev script", false, 0},
		{"dev is not executable", true, 0o644},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, clone, f, wt := worktreeFixture(t, tt.withDev, tt.mode)
			got, err := r.CreateWorktree(clone, 7)
			if err != nil || got.Path != wt || got.Branch != "task/r-7" {
				t.Fatalf("worktree = %+v, %v", got, err)
			}
			want := "git worktree add --quiet --no-track -b task/r-7 " + wt + " origin/main"
			if !f.has(want) {
				t.Errorf("commands %v lack %q", f.lines(), want)
			}
		})
	}
}

func TestCreateWorktreeRefusals(t *testing.T) {
	repo := mustRepo(t, "o/r")
	tests := []struct {
		name    string
		task    int
		replies []reply
		wantErr string
	}{
		{"task zero", 0, nil, "must be positive"},
		{"worktree exists", 7, []reply{{key: "worktree list", out: porcelain("/c", "/old", "task/r-7")}}, "already has a worktree at /old"},
		{"branch exists", 7, []reply{{key: "rev-parse --verify", out: "abc"}}, "already exists"},
		{"origin has the branch", 7, []reply{{key: "refs/remotes/origin/task/r-7", out: "abc"}}, "origin already has branch task/r-7"},
		{"list fails", 7, []reply{{key: "worktree list", err: errBoom}}, "list worktrees"},
		{"git worktree add fails", 7, []reply{{key: "worktree add", err: errBoom}}, "create the worktree for task/r-7"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &gitFake{replies: tt.replies}
			f.replies = append(f.replies, reply{key: "rev-parse --verify", err: errBoom})
			r := newRepos(t, f)
			path, _ := r.Path(repo)
			if err := os.MkdirAll(path, 0o755); err != nil {
				t.Fatal(err)
			}
			_, err := r.CreateWorktree(Clone{Repo: repo, Path: path, Default: "main"}, tt.task)
			var takenErr *BranchTakenError
			if want := strings.Contains(tt.name, "exists") || strings.Contains(tt.name, "has the branch"); errors.As(err, &takenErr) != want {
				t.Errorf("a taken branch is a BranchTakenError, and nothing else is: %v", err)
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %v, want it to contain %q", err, tt.wantErr)
			}
			if tt.wantErr != "create the worktree for task/r-7" && f.has("worktree add") {
				t.Error("created a worktree despite refusing")
			}
		})
	}
}

func TestNothingRunsWithoutAToken(t *testing.T) {
	repo := mustRepo(t, "o/r")
	for _, token := range []string{"", " \n"} {
		f := &gitFake{replies: syncReplies()}
		r := newRepos(t, f)
		r.Token = token
		if _, err := r.Sync(repo); err == nil || !strings.Contains(err.Error(), "no machine-user token") {
			t.Errorf("Sync with token %q: %v", token, err)
		}
		clone := Clone{Repo: repo, Path: filepath.Join(r.Home, "clone"), Default: "main"}
		if _, err := r.CreateWorktree(clone, 7); err == nil || !strings.Contains(err.Error(), "no machine-user token") {
			t.Errorf("CreateWorktree with token %q: %v", token, err)
		}
		if len(f.calls) != 0 {
			t.Errorf("ran %v without a token: gh would act as the operator", f.lines())
		}
	}
}

func TestCreateWorktreeFailsWhenTheCommandMakesNone(t *testing.T) {
	repo := mustRepo(t, "o/r")
	f := &gitFake{replies: []reply{{key: "rev-parse --verify", err: errBoom}}}
	r := newRepos(t, f)
	path, _ := r.Path(repo)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := r.CreateWorktree(Clone{Repo: repo, Path: path, Default: "main"}, 7)
	if err == nil || !strings.Contains(err.Error(), "no worktree has branch task/r-7") {
		t.Errorf("err = %v", err)
	}
}

func TestCreateWorktreeFailsWhenTheLockCannotBeTaken(t *testing.T) {
	f := &gitFake{}
	r := newRepos(t, f)
	blocker := filepath.Join(r.Home, "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	clone := Clone{Repo: mustRepo(t, "o/r"), Path: filepath.Join(blocker, "clone"), Default: "main"}
	if _, err := r.CreateWorktree(clone, 7); err == nil || len(f.calls) != 0 {
		t.Errorf("err %v, commands %v", err, f.lines())
	}
}
