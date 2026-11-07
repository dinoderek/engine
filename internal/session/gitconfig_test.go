package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyncRestoresInheritedGitConfig(t *testing.T) {
	remote := newRemote(t, "main")
	remote.commit(t, "main", "a", "a", 0o644)
	r := remote.repos(t)
	repo := mustRepo(t, "o/r")
	clone, err := r.Sync(repo)
	if err != nil {
		t.Fatal(err)
	}
	// Only the exact generated bundle is removed; unrelated config survives.
	for _, args := range [][]string{
		{"--add", "credential.helper", ""}, {"--add", "credential.helper", "!gh auth git-credential"},
		{"user.name", "developer"}, {"user.email", "42+developer@users.noreply.github.com"},
		{"core.autocrlf", "false"},
	} {
		mustGit(t, clone.Path, append([]string{"config", "--local"}, args...)...)
	}
	if _, err = r.Sync(repo); err != nil {
		t.Fatal(err)
	}
	config := mustGit(t, clone.Path, "config", "--local", "--list")
	for _, key := range []string{"credential.helper=", "user.name=", "user.email="} {
		if strings.Contains(config, key) {
			t.Fatalf("override remains: %s", config)
		}
	}
	if !strings.Contains(config, "core.autocrlf=false") {
		t.Fatal(config)
	}
	if _, err = r.Sync(repo); err != nil {
		t.Fatal(err)
	}
}

func TestSyncPreservesOperatorGitConfig(t *testing.T) {
	for _, tc := range []struct {
		name, email string
		helpers     []string
	}{
		{"Developer", "developer@example.com", []string{"", "!gh auth git-credential"}},
		{"developer", "42+developer@users.noreply.github.com", []string{"custom-helper"}},
		{"developer", "42+another@users.noreply.github.com", []string{"", "!gh auth git-credential"}},
	} {
		t.Run(tc.email+strings.Join(tc.helpers, ""), func(t *testing.T) {
			remote := newRemote(t, "main")
			remote.commit(t, "main", "a", "a", 0o644)
			r := remote.repos(t)
			repo := mustRepo(t, "o/r")
			clone, err := r.Sync(repo)
			if err != nil {
				t.Fatal(err)
			}
			mustGit(t, clone.Path, "config", "--local", "user.name", tc.name)
			mustGit(t, clone.Path, "config", "--local", "user.email", tc.email)
			for _, h := range tc.helpers {
				mustGit(t, clone.Path, "config", "--local", "--add", "credential.helper", h)
			}
			before := mustGit(t, clone.Path, "config", "--local", "--list")
			if _, err = r.Sync(repo); err != nil {
				t.Fatal(err)
			}
			if after := mustGit(t, clone.Path, "config", "--local", "--list"); after != before {
				t.Fatalf("configuration changed:\n%s", after)
			}
		})
	}
}

func TestInheritedConfigFailures(t *testing.T) {
	bundle := "credential.helper=\ncredential.helper=!gh auth git-credential\nuser.name=developer\nuser.email=42+developer@users.noreply.github.com"
	for _, key := range []string{"config --local --list", "--unset-all credential.helper", "--unset-all user.name", "--unset-all user.email"} {
		t.Run(key, func(t *testing.T) {
			fake := &gitFake{replies: []reply{{key: key, err: errBoom}, {key: "config --local --list", out: bundle}}}
			r := Repos{Run: fake.run}
			if err := r.inheritGitConfig(t.TempDir()); err == nil {
				t.Fatal("configuration failure ignored")
			}
		})
	}
	// A real unreadable config is a failure, not permission to change identity.
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := (Repos{Run: realGit}).inheritGitConfig(dir); err == nil {
		t.Fatal("invalid clone accepted")
	}
}
