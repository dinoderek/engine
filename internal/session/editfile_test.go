package session

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writerFor is a stamp check that sees another writer change the file the
// first n times engine is about to rename it.
func writerFor(n int) func(string) (Stamp, error) {
	return func(path string) (Stamp, error) {
		st, err := FileStamp(path)
		if n > 0 {
			n--
			st.size++
		}
		return st, err
	}
}

func TestEditFileRetriesThenGivesUpOnABusyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("a\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	add := func(data []byte) ([]byte, error) { return append(data, "b\n"...), nil }
	if err := EditFile(path, writerFor(editAttempts-1), add); err != nil {
		t.Fatal(err)
	}
	if got := readFile(path); got != "a\nb\n" {
		t.Errorf("the last attempt should succeed: %q", got)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o640 {
		t.Errorf("mode %v", info.Mode())
	}
	err := EditFile(path, writerFor(editAttempts), add)
	if !errors.Is(err, ErrKeptChanging) || !strings.HasSuffix(err.Error(), "/f kept changing while engine updated it") {
		t.Errorf("err = %v", err)
	}
	if got := readFile(path); got != "a\nb\n" {
		t.Errorf("a busy file was overwritten: %q", got)
	}
	if left, _ := filepath.Glob(path + ".engine-*"); len(left) > 0 {
		t.Errorf("temp files left: %v", left)
	}
}

func TestEditFileLeavesAFileAloneOrReportsAnEditError(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing")
	var saw []byte
	if err := EditFile(missing, nil, func(data []byte) ([]byte, error) { saw = data; return nil, nil }); err != nil || saw != nil {
		t.Errorf("missing file: %v, %q", err, saw)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Errorf("a no-op edit made the file: %v", err)
	}
	err := EditFile(missing, nil, func([]byte) ([]byte, error) { return nil, errors.New("bad") })
	if err == nil || !strings.HasSuffix(err.Error(), "/missing: bad") {
		t.Errorf("edit error: %v", err)
	}
	if err := EditFile(missing, nil, func([]byte) ([]byte, error) { return []byte("new"), nil }); err != nil || readFile(missing) != "new\n" {
		t.Errorf("new file: %v, %q", err, readFile(missing))
	}
	if info, _ := os.Stat(missing); info.Mode().Perm() != 0o600 {
		t.Errorf("a new file's mode %v", info.Mode())
	}
}

func TestEditFileFollowsASymlink(t *testing.T) {
	dir := t.TempDir()
	target, link := filepath.Join(dir, "target"), filepath.Join(dir, "link")
	if err := os.WriteFile(target, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := EditFile(link, nil, func(d []byte) ([]byte, error) { return append(d, "y"...), nil }); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the link was replaced: %v", err)
	}
	if got := readFile(target); got != "x\ny\n" {
		t.Errorf("target %q", got)
	}
}

func TestWriteFileAndTrustKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	if err := WriteFile(path, []byte("one\n"), 0o600); err != nil || readFile(path) != "one\n" {
		t.Errorf("write: %v, %q", err, readFile(path))
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(dir)
	if got := TrustKeys(link); len(got) != 2 || got[0] != link || got[1] != real {
		t.Errorf("keys %v", got)
	}
	if got := TrustKeys(real); len(got) != 1 {
		t.Errorf("keys %v", got)
	}
	if !ValidName("engine-r-7") || ValidName("..") {
		t.Error("ValidName")
	}
}
