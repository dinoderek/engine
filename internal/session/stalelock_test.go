package session

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestCreateWorktreeKeepsALockAGitCommitHoldsInAnEditor: a commit waiting on
// its editor holds index.lock with the file closed, so only its cwd shows it.
// The worktree's path is plain, and also one with a space, which lsof prints
// as a single line.
func TestCreateWorktreeKeepsALockAGitCommitHoldsInAnEditor(t *testing.T) {
	for _, spaced := range []bool{false, true} {
		name := "plain path"
		if spaced {
			name = "path with a space"
		}
		t.Run(name, func(t *testing.T) {
			r, clone, first, _ := ownFixture(t)
			if spaced {
				dst := filepath.Join(t.TempDir(), "a b")
				mustGit(t, clone.Path, "worktree", "move", first.Path, dst)
				first.Path = dst
			}
			holdLockInEditor(t, first.Path)
			lock := lockOf(t, first.Path)
			_, err := r.CreateWorktree(clone, 5, true, "r-5-a2")
			var takenErr *BranchTakenError
			if !errors.As(err, &takenErr) || !strings.Contains(err.Error(), "held by a running process") {
				t.Fatalf("a lock a git commit holds: err = %v, want a refusal that names it", err)
			}
			if _, err := os.Stat(lock); err != nil {
				t.Errorf("a lock a git commit holds was removed: %v", err)
			}
		})
	}
}

// holdLockInEditor starts a commit in dir that waits on an editor, and ends
// the commit and its editor when the test does.
func holdLockInEditor(t *testing.T, dir string) {
	t.Helper()
	// A change to stage makes the commit write the index, and so take its lock.
	if err := os.WriteFile(filepath.Join(dir, "work.txt"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	editor := filepath.Join(t.TempDir(), "editor")
	if err := os.WriteFile(editor, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	commit := exec.Command("git", "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "--quiet", "-a")
	commit.Dir = dir
	commit.Env = append(os.Environ(), "GIT_EDITOR="+editor)
	// Its own process group, so the editor's sleep dies with the commit.
	commit.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := commit.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-commit.Process.Pid, syscall.SIGKILL)
		_ = commit.Wait()
	})
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(lockOf(t, dir)); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the commit never took the index lock")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestHeldOpenFailsWithoutLsof(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	lock := filepath.Join(t.TempDir(), "index.lock")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if held, err := heldOpen(lock, t.TempDir()); err == nil || held {
		t.Fatalf("heldOpen without lsof = %v, %v; want an error, so no lock is removed on a guess", held, err)
	}
}
