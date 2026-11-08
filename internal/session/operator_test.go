package session

import (
	"strings"
	"testing"
)

func TestOperatorCloneUsesNormalGitIdentityAndSeparateStorage(t *testing.T) {
	remote := newRemote(t, "main")
	remote.commit(t, "main", "a", "a", 0o644)
	var calls []string
	runner := func(dir string, env []string, name string, args ...string) (string, error) {
		line := name + " " + strings.Join(args, " ")
		calls = append(calls, line)
		if name == "gh" {
			t.Fatal("ordinary git sync called GitHub API")
		}
		if strings.Join(env, " ") != "GIT_TERMINAL_PROMPT=0" {
			t.Fatalf("environment overridden: %v", env)
		}
		return realGit(dir, env, name, args...)
	}
	repos := Repos{Run: runner, Home: t.TempDir(), Control: true, RemoteURL: remote.bare}
	clone, err := repos.Sync(mustRepo(t, "o/r"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(clone.Path, ControlReposDir) {
		t.Fatal("server clone shares GitHub-plan storage")
	}
	if helpers(t, clone.Path) != "" {
		t.Fatal("operator credential helpers overwritten")
	}
	wt, err := repos.CreateWorktree(clone, 7, false)
	if err != nil {
		t.Fatal(err)
	}
	if wt.Branch != "task/r-7" {
		t.Fatal(wt)
	}
	for _, line := range calls {
		if strings.Contains(line, "config --local user.") {
			t.Fatal("operator identity overwritten")
		}
	}
}
