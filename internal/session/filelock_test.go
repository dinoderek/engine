package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeLock is the lock on path with a clock that only sleep moves; each
// sleep calls slept, when it is not nil, with the new time before it returns.
func fakeLock(path string, slept func(time.Time)) fileLock {
	now := time.Unix(1_000_000, 0)
	l := lockFor(path)
	l.now = func() time.Time { return now }
	l.sleep = func(d time.Duration) {
		now = now.Add(d)
		if slept != nil {
			slept(now)
		}
	}
	return l
}

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func appendLine(line string) func([]byte) ([]byte, error) {
	return func(data []byte) ([]byte, error) { return append(data, line+"\n"...), nil }
}

// A writer that holds the lock, as Claude Code does around a save it bases
// on its own read, is waited for: engine's edit lands on what it wrote, and
// is not overwritten by it.
func TestEditFileWaitsForTheLockHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	writeFile(t, path, "a\n")
	if err := os.Mkdir(path+".lock", 0o700); err != nil {
		t.Fatal(err)
	}
	read := readFile(path)
	waiting, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	slept := func(time.Time) { once.Do(func() { close(waiting); <-resume }) }
	done := make(chan error)
	go func() { done <- editFile(fakeLock(path, slept), path, nil, appendLine("engine")) }()
	<-waiting // engine waits on the lock
	writeFile(t, path, read+"holder\n")
	if err := os.Remove(path + ".lock"); err != nil {
		t.Fatal(err)
	}
	close(resume)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := readFile(path); got != "a\nholder\nengine\n" {
		t.Errorf("file = %q", got)
	}
	if _, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
		t.Errorf("the lock was not released: %v", err)
	}
}

// A lock older than lockStale is a dead holder's, which engine takes over;
// a live one held past lockWait fails the edit and leaves the file alone.
func TestEditFileTakesOverAStaleLockAndGivesUpOnAHeldOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	writeFile(t, path, "a\n")
	if err := os.Mkdir(path+".lock", 0o700); err != nil {
		t.Fatal(err)
	}
	// A live holder refreshes the lock while it holds it.
	refresh := func(now time.Time) {
		if err := os.Chtimes(path+".lock", now, now); err != nil {
			t.Error(err)
		}
	}
	l := fakeLock(path, refresh)
	refresh(l.now())
	err := editFile(l, path, nil, appendLine("b"))
	if !errors.Is(err, ErrLockHeld) || !strings.Contains(err.Error(), "f.lock stayed locked by another writer for 30s") {
		t.Errorf("held: %v", err)
	}
	if got := readFile(path); got != "a\n" {
		t.Errorf("a locked file was written: %q", got)
	}
	// A stale lock is taken over on the first look, without waiting.
	stale := fakeLock(path, func(time.Time) { t.Error("waited on a stale lock") })
	old := stale.now().Add(-lockStale - time.Second)
	if err := os.Chtimes(path+".lock", old, old); err != nil {
		t.Fatal(err)
	}
	if err := editFile(stale, path, nil, appendLine("b")); err != nil {
		t.Fatal(err)
	}
	if got := readFile(path); got != "a\nb\n" {
		t.Errorf("stale: %q", got)
	}
}

// The lock is released whatever the edit does.
func TestEditFileReleasesTheLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	edits := []func([]byte) ([]byte, error){
		appendLine("b"),
		func([]byte) ([]byte, error) { return nil, nil },
		func([]byte) ([]byte, error) { return nil, errors.New("bad") },
	}
	for i, edit := range edits {
		_ = EditFile(path, nil, edit)
		if _, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
			t.Errorf("edit %d left the lock: %v", i, err)
		}
	}
}

// A file in a missing folder is shared by nobody: an edit that leaves it
// missing needs no lock, and one that would write it fails on the lock.
func TestEditFileInAMissingFolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "none", "f")
	if err := EditFile(path, nil, func([]byte) ([]byte, error) { return nil, nil }); err != nil {
		t.Errorf("no-op: %v", err)
	}
	if err := EditFile(path, nil, func([]byte) ([]byte, error) { return nil, errors.New("bad") }); err == nil || !strings.HasSuffix(err.Error(), "/f: bad") {
		t.Errorf("edit error: %v", err)
	}
	if err := EditFile(path, nil, appendLine("b")); err == nil || !strings.Contains(err.Error(), "lock "+path+".lock") {
		t.Errorf("err = %v", err)
	}
}

// Concurrent edits, as two spawns on one host make, each keep the others'.
func TestConcurrentEditsKeepEveryChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	const writers = 8
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := range writers {
		wg.Go(func() { errs <- EditFile(path, nil, appendLine(fmt.Sprint("w", i))) })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	got := readFile(path)
	for i := range writers {
		if !strings.Contains(got, fmt.Sprintf("w%d\n", i)) {
			t.Errorf("w%d lost:\n%s", i, got)
		}
	}
}
