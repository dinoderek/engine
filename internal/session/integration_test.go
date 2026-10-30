package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The tests here run real git against a bare repository in a temp dir. The
// "remote" is a file path, so they never touch the network or a real clone.

var isolatedGit = []string{
	"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
	"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
	"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
}

// realGit runs real git, isolated from the host's git config, and answers gh
// with a fixed machine user.
func realGit(dir string, env []string, name string, args ...string) (string, error) {
	if name == "gh" {
		return "testbot 42\n", nil
	}
	return RunCommand(dir, append(append([]string{}, env...), isolatedGit...), name, args...)
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := realGit(dir, nil, "git", args...)
	if err != nil {
		t.Fatalf("git %v in %s: %v", args, dir, err)
	}
	return strings.TrimSpace(out)
}

// remoteFixture is a bare "o/r.git" under base, and a work clone to push from.
type remoteFixture struct {
	base, bare, work string
}

func newRemote(t *testing.T, branch string) remoteFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	f := remoteFixture{base: filepath.Join(root, "remote"), work: filepath.Join(root, "work")}
	f.bare = filepath.Join(f.base, "o", "r.git")
	for _, d := range []string{f.bare, f.work} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustGit(t, f.bare, "init", "--bare", "--quiet", "--initial-branch="+branch)
	mustGit(t, f.work, "init", "--quiet", "--initial-branch="+branch)
	mustGit(t, f.work, "remote", "add", "origin", f.bare)
	return f
}

// commit writes a file in the work clone, commits it and pushes branch.
func (f remoteFixture) commit(t *testing.T, branch, file, content string, mode os.FileMode) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.work, file), []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	mustGit(t, f.work, "add", file)
	mustGit(t, f.work, "commit", "--quiet", "-m", "add "+file)
	mustGit(t, f.work, "push", "--quiet", "origin", "HEAD:refs/heads/"+branch)
	return mustGit(t, f.work, "rev-parse", "HEAD")
}

func (f remoteFixture) repos(t *testing.T) Repos {
	t.Helper()
	return Repos{Run: realGit, Home: t.TempDir(), Token: "tok", BaseURL: "file://" + f.base}
}

// helpers lists the clone's local credential.helper settings, in order.
func helpers(t *testing.T, dir string) string {
	t.Helper()
	var found []string
	for _, line := range strings.Split(mustGit(t, dir, "config", "--local", "--list"), "\n") {
		if strings.HasPrefix(line, "credential.helper=") {
			found = append(found, line)
		}
	}
	return strings.Join(found, "|")
}

func samePath(t *testing.T, a, b string) bool {
	t.Helper()
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

func TestSyncClonesConfiguresAndFollowsTheRemote(t *testing.T) {
	remote := newRemote(t, "main")
	first := remote.commit(t, "main", "a.txt", "a", 0o644)
	r := remote.repos(t)
	repo := mustRepo(t, "o/r")

	clone, err := r.Sync(repo)
	if err != nil {
		t.Fatal(err)
	}
	if clone.Default != "main" || !strings.HasSuffix(clone.Path, filepath.FromSlash(".local/share/engine/repos/o/r")) {
		t.Errorf("clone = %+v", clone)
	}
	if got := mustGit(t, clone.Path, "rev-parse", "HEAD"); got != first {
		t.Errorf("HEAD = %s, want %s", got, first)
	}
	if got := mustGit(t, clone.Path, "config", "--local", "user.name"); got != "testbot" {
		t.Errorf("user.name = %q", got)
	}
	if got := mustGit(t, clone.Path, "config", "--local", "user.email"); got != "42+testbot@users.noreply.github.com" {
		t.Errorf("user.email = %q", got)
	}
	// An empty helper first, so a global helper cannot supply other credentials.
	if got := helpers(t, clone.Path); got != "credential.helper=|credential.helper=!gh auth git-credential" {
		t.Errorf("credential helpers = %q", got)
	}

	// New commits on the remote, and a changed default branch, are followed.
	mustGit(t, remote.work, "checkout", "--quiet", "-b", "trunk")
	second := remote.commit(t, "trunk", "b.txt", "b", 0o644)
	mustGit(t, remote.bare, "symbolic-ref", "HEAD", "refs/heads/trunk")
	again, err := r.Sync(repo)
	if err != nil {
		t.Fatal(err)
	}
	if again.Path != clone.Path || again.Default != "trunk" {
		t.Errorf("second sync = %+v", again)
	}
	if got := mustGit(t, clone.Path, "rev-parse", "HEAD"); got != second {
		t.Errorf("HEAD = %s after the second sync, want %s", got, second)
	}
	if got := helpers(t, clone.Path); got != "credential.helper=|credential.helper=!gh auth git-credential" {
		t.Errorf("credential helpers after a second sync = %q", got)
	}
}

// rewriting is a RunFunc whose git has a host-level rule, as a global
// ~/.gitconfig would, that rewrites the remote's URL to somewhere else:
// kind is "insteadOf" or "pushInsteadOf".
func rewriting(remote remoteFixture, kind string) RunFunc {
	rule := []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=url.file:///elsewhere/." + kind,
		"GIT_CONFIG_VALUE_0=file://" + remote.base + "/",
	}
	return func(dir string, env []string, name string, args ...string) (string, error) {
		return realGit(dir, append(append([]string{}, env...), rule...), name, args...)
	}
}

func TestSyncRefusesAHostRuleThatRewritesTheRemote(t *testing.T) {
	for _, kind := range []string{"insteadOf", "pushInsteadOf"} {
		t.Run(kind, func(t *testing.T) {
			remote := newRemote(t, "main")
			remote.commit(t, "main", "a.txt", "a", 0o644)
			r := remote.repos(t)
			r.Run = rewriting(remote, kind)
			repo := mustRepo(t, "o/r")
			_, err := r.Sync(repo)
			if err == nil || !strings.Contains(err.Error(), "git rewrites") || !strings.Contains(err.Error(), "remove the rule") {
				t.Fatalf("first sync: err = %v", err)
			}
			path, _ := r.Path(repo)
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("a refused clone was left at %s", path)
			}
			// A clone made before the rule existed is refused too, with the cause named.
			r.Run = realGit
			if _, err := r.Sync(repo); err != nil {
				t.Fatal(err)
			}
			r.Run = rewriting(remote, kind)
			if _, err := r.Sync(repo); err == nil || !strings.Contains(err.Error(), "git rewrites") {
				t.Errorf("existing clone: err = %v", err)
			}
		})
	}
}

func TestSyncRefusesAnotherRepoInThePlace(t *testing.T) {
	remote := newRemote(t, "main")
	remote.commit(t, "main", "a.txt", "a", 0o644)
	r := remote.repos(t)
	repo := mustRepo(t, "o/r")
	clone, err := r.Sync(repo)
	if err != nil {
		t.Fatal(err)
	}
	mustGit(t, clone.Path, "remote", "set-url", "origin", "file:///elsewhere/o/other.git")
	if _, err := r.Sync(repo); err == nil || !strings.Contains(err.Error(), "is not a clone of") {
		t.Errorf("err = %v", err)
	}
}

func TestSyncReportsAMissingRemote(t *testing.T) {
	remote := newRemote(t, "main")
	r := remote.repos(t)
	_, err := r.Sync(mustRepo(t, "o/missing"))
	if err == nil || !strings.Contains(err.Error(), "clone o/missing") {
		t.Fatalf("err = %v", err)
	}
	path, _ := r.Path(mustRepo(t, "o/missing"))
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("a failed clone left %s behind", path)
	}
}

func TestCreateWorktreeFallbackForReal(t *testing.T) {
	remote := newRemote(t, "main")
	tip := remote.commit(t, "main", "a.txt", "a", 0o644)
	r := remote.repos(t)
	clone, err := r.Sync(mustRepo(t, "o/r"))
	if err != nil {
		t.Fatal(err)
	}
	wt, err := r.CreateWorktree(clone, 412)
	if err != nil {
		t.Fatal(err)
	}
	if wt.Branch != "task/r-412" || !samePath(t, wt.Path, clone.Path+"-worktrees/task-r-412") {
		t.Errorf("worktree = %+v", wt)
	}
	if got := mustGit(t, wt.Path, "rev-parse", "--abbrev-ref", "HEAD"); got != "task/r-412" {
		t.Errorf("worktree is on %q", got)
	}
	if got := mustGit(t, wt.Path, "rev-parse", "HEAD"); got != tip {
		t.Errorf("worktree is at %s, want origin's default branch tip %s", got, tip)
	}
	// The clone's identity applies in the worktree, because git shares local config.
	if got := mustGit(t, wt.Path, "config", "user.email"); got != "42+testbot@users.noreply.github.com" {
		t.Errorf("worktree user.email = %q", got)
	}
	if _, err := r.CreateWorktree(clone, 412); err == nil || !strings.Contains(err.Error(), "already has a worktree") {
		t.Errorf("second create: %v", err)
	}
}

func TestCreateWorktreeRemakesOnlyALeftoverBranchWithNoWork(t *testing.T) {
	remote := newRemote(t, "main")
	remote.commit(t, "main", "a.txt", "a", 0o644)
	r := remote.repos(t)
	clone, err := r.Sync(mustRepo(t, "o/r"))
	if err != nil {
		t.Fatal(err)
	}
	mustGit(t, clone.Path, "branch", "task/r-3", "origin/main")
	mustGit(t, clone.Path, "branch", "task/r-4", "origin/main")
	mustGit(t, clone.Path, "worktree", "add", "--quiet", clone.Path+"-wt4", "task/r-4")
	mustGit(t, clone.Path+"-wt4", "commit", "--quiet", "--allow-empty", "-m", "work")
	mustGit(t, clone.Path, "worktree", "remove", clone.Path+"-wt4")
	if _, err := r.CreateWorktree(clone, 4); err == nil || !strings.Contains(err.Error(), "with commits of its own") {
		t.Errorf("a branch with work: err = %v", err)
	}
	// A branch with no commits of its own, as a session that died before
	// claiming leaves, is made again from the default branch.
	remote.commit(t, "main", "b.txt", "b", 0o644)
	if _, err := r.Sync(mustRepo(t, "o/r")); err != nil {
		t.Fatal(err)
	}
	wt, err := r.CreateWorktree(clone, 3)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := mustGit(t, wt.Path, "rev-parse", "HEAD"), mustGit(t, clone.Path, "rev-parse", "origin/main"); got != want {
		t.Errorf("the branch was made at %s, want origin/main %s", got, want)
	}
}

func TestCreateWorktreeRunsTheReposOwnCommandForReal(t *testing.T) {
	remote := newRemote(t, "main")
	// The repo's command puts the worktree somewhere of its own choosing.
	// With no verb it prints its usage, which is how engine learns it has one.
	script := "#!/bin/sh\nset -e\ncd \"$(dirname \"$0\")\"\n" +
		"if [ $# -eq 0 ]; then echo 'usage: ./dev worktree create <branch>' >&2; exit 2; fi\n" +
		"test \"$1 $2\" = 'worktree create'\n" +
		"git worktree add --quiet --no-track -b \"$3\" \"$PWD-custom\" origin/main\n"
	remote.commit(t, "main", "dev", script, 0o755)
	r := remote.repos(t)
	clone, err := r.Sync(mustRepo(t, "o/r"))
	if err != nil {
		t.Fatal(err)
	}
	wt, err := r.CreateWorktree(clone, 9)
	if err != nil {
		t.Fatal(err)
	}
	if !samePath(t, wt.Path, clone.Path+"-custom") || wt.Branch != "task/r-9" {
		t.Errorf("worktree = %+v: the path must come from git, not a guess", wt)
	}
	if got := mustGit(t, wt.Path, "rev-parse", "--abbrev-ref", "HEAD"); got != "task/r-9" {
		t.Errorf("worktree is on %q", got)
	}
}

func TestCreateWorktreeFallsBackWhenTheReposDevNeverAnswers(t *testing.T) {
	remote := newRemote(t, "main")
	// Run with no verb, this dev starts something long-running instead of printing usage.
	remote.commit(t, "main", "dev", "#!/bin/sh\nsleep 30\n", 0o755)
	r := remote.repos(t)
	r.Probe = RunCommandWithin(300 * time.Millisecond)
	clone, err := r.Sync(mustRepo(t, "o/r"))
	if err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	wt, err := r.CreateWorktree(clone, 4)
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(began); took > 15*time.Second {
		t.Errorf("took %s: the probe must give up at its deadline", took)
	}
	if !samePath(t, wt.Path, clone.Path+"-worktrees/task-r-4") {
		t.Errorf("worktree = %+v, want git's fallback path", wt)
	}
}
