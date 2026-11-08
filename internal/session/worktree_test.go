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
		{key: "worktree create"},
		{key: filepath.Join(path, "dev"), out: devUsage, err: errBoom},
	}
	f.onRun = func(c call) {
		if strings.Contains(c.line, "worktree create") || strings.Contains(c.line, "worktree add") {
			for i := range f.replies {
				if f.replies[i].key == "worktree list" {
					f.replies[i].out = porcelain(path, wt, "task/r-7")
				}
			}
		}
	}
	return r, Clone{Repo: repo, Path: path, Default: "main"}, f, wt
}

func TestCreateWorktreeWithTheReposOwnCommand(t *testing.T) {
	r, clone, f, wt := worktreeFixture(t, true, 0o755)
	got, err := r.CreateWorktree(clone, 7, false)
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
	if !reflect.DeepEqual(ran[0].env, []string{"GIT_TERMINAL_PROMPT=0"}) {
		t.Errorf("env = %v", ran[0].env)
	}
	if f.has("worktree add") {
		t.Error("git worktree add ran besides the repo's command")
	}
	var probes []call
	for _, c := range f.calls {
		if c.line == script {
			probes = append(probes, c)
		}
	}
	if len(probes) != 1 || probes[0].dir != clone.Path || !reflect.DeepEqual(probes[0].env, ran[0].env) {
		t.Errorf("the usage probe ran as %+v", probes)
	}
}

func TestCreateWorktreeProbesWithProbeWhenSet(t *testing.T) {
	r, clone, f, wt := worktreeFixture(t, true, 0o755)
	var probed []string
	// A dev that never answers: the probe gives up with no usage.
	r.Probe = func(dir string, env []string, name string, args ...string) (string, error) {
		probed = append(probed, strings.Join(append([]string{name}, args...), " "))
		return "", errBoom
	}
	got, err := r.CreateWorktree(clone, 7, false)
	if err != nil || got.Path != wt {
		t.Fatalf("worktree = %+v, %v", got, err)
	}
	if want := []string{filepath.Join(clone.Path, "dev")}; !reflect.DeepEqual(probed, want) {
		t.Errorf("probe ran %v, want %v", probed, want)
	}
	if f.has("worktree create") || !f.has("git worktree add") {
		t.Errorf("a dev whose probe failed must fall back to git: %v", f.lines())
	}
}

// devUsage is what a `dev` that provides worktrees prints when run with no verb.
const devUsage = "usage: ./dev <verb>\n\n  worktree create <branch>     new branch and worktree\n"

func TestCreateWorktreeFallsBackToGitWhenTheReposDevHasNoWorktreeVerb(t *testing.T) {
	r, clone, f, wt := worktreeFixture(t, true, 0o755)
	// A repo's own unrelated ./dev, here an argparse script with other verbs.
	f.replies[3].out = "usage: ./dev [-h] {test,docs} ...\n./dev: error: the following arguments are required: command\n"
	got, err := r.CreateWorktree(clone, 7, false)
	if err != nil || got.Path != wt || got.Branch != "task/r-7" {
		t.Fatalf("worktree = %+v, %v", got, err)
	}
	if f.has("worktree create") {
		t.Errorf("ran the repo's dev worktree create though its usage lacks it: %v", f.lines())
	}
	want := "git worktree add --quiet --no-track -b task/r-7 " + wt + " origin/main"
	if !f.has(want) {
		t.Errorf("commands %v lack %q", f.lines(), want)
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
			got, err := r.CreateWorktree(clone, 7, false)
			if err != nil || got.Path != wt || got.Branch != "task/r-7" {
				t.Fatalf("worktree = %+v, %v", got, err)
			}
			want := "git worktree add --quiet --no-track -b task/r-7 " + wt + " origin/main"
			if !f.has(want) {
				t.Errorf("commands %v lack %q", f.lines(), want)
			}
			if f.has(filepath.Join(clone.Path, "dev")) {
				t.Errorf("probed a dev that is not executable: %v", f.lines())
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
		{"branch exists", 7, []reply{{key: "merge-base", err: errBoom}, {key: "refs/heads/task/r-7", out: "abc"}}, "with commits of its own"},
		{"leftover branch delete fails", 7, []reply{{key: "refs/heads/task/r-7", out: "abc"}, {key: "branch -D", err: errBoom}}, "delete the leftover branch task/r-7"},
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
			_, err := r.CreateWorktree(Clone{Repo: repo, Path: path, Default: "main"}, tt.task, false)
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

func TestCreateWorktreeFailsWhenTheCommandMakesNone(t *testing.T) {
	repo := mustRepo(t, "o/r")
	f := &gitFake{replies: []reply{{key: "rev-parse --verify", err: errBoom}}}
	r := newRepos(t, f)
	path, _ := r.Path(repo)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := r.CreateWorktree(Clone{Repo: repo, Path: path, Default: "main"}, 7, false)
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
	if _, err := r.CreateWorktree(clone, 7, false); err == nil || len(f.calls) != 0 {
		t.Errorf("err %v, commands %v", err, f.lines())
	}
}

func TestCreateWorktreeOnResumeOpensOnOriginsBranch(t *testing.T) {
	for _, withDev := range []bool{false, true} {
		r, clone, f, wt := worktreeFixture(t, withDev, 0o755)
		// Origin has the branch, and the worktree opens at its tip.
		f.replies = append([]reply{{key: "refs/remotes/origin/task/r-7", out: "abc"}, {key: "rev-parse HEAD", out: "abc\n"}}, f.replies...)
		got, err := r.CreateWorktree(clone, 7, true)
		if err != nil || got.Path != wt {
			t.Fatalf("dev %v: worktree = %+v, %v", withDev, got, err)
		}
		if !f.has("git branch --force --no-track task/r-7 refs/remotes/origin/task/r-7") {
			t.Errorf("dev %v: the local branch was not set to origin's tip: %v", withDev, f.lines())
		}
		create := "git worktree add --quiet " + wt + " task/r-7"
		if withDev {
			create = filepath.Join(clone.Path, "dev") + " worktree create task/r-7"
		}
		if !f.has(create) || f.has("-b task/r-7") {
			t.Errorf("dev %v: commands %v lack %q, or branch anew", withDev, f.lines(), create)
		}
	}
}

func TestCreateWorktreeOnResumeFailsWhenTheBranchCannotBeSet(t *testing.T) {
	r, clone, f, _ := worktreeFixture(t, false, 0)
	f.replies = append([]reply{{key: "branch --force", err: errBoom}, {key: "refs/remotes/origin/task/r-7", out: "abc"}}, f.replies...)
	_, err := r.CreateWorktree(clone, 7, true)
	var takenErr *BranchTakenError
	if err == nil || errors.As(err, &takenErr) || !strings.Contains(err.Error(), "set branch task/r-7 to origin's tip") {
		t.Errorf("err = %v", err)
	}
	if f.has("worktree add") {
		t.Error("created a worktree though the branch was not set")
	}
}

func TestCreateWorktreeOnResumeFailsWhenTheReposCommandMovesTheBranch(t *testing.T) {
	r, clone, f, _ := worktreeFixture(t, true, 0o755)
	// The repo's command made the branch anew from the default branch.
	f.replies = append([]reply{{key: "refs/remotes/origin/task/r-7", out: "abc"}, {key: "rev-parse HEAD", out: "main-tip\n"}}, f.replies...)
	if _, err := r.CreateWorktree(clone, 7, true); err == nil || !strings.Contains(err.Error(), "not at origin's tip of task/r-7") {
		t.Errorf("err = %v", err)
	}
}
