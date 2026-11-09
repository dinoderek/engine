package session

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Stamp tells whether a file changed: a missing file has the zero stamp.
type Stamp = fileStamp

// FileStamp is the stamp of the file at path.
func FileStamp(path string) (Stamp, error) { return stamp(path) }

// ErrKeptChanging is an edit that gave up because another writer kept
// changing the file.
var ErrKeptChanging = errors.New("kept changing while engine updated it")

// editAttempts bounds the retries when another writer keeps changing a file.
const editAttempts = 3

// EditFile rewrites the file at path with edit, which gets the file's bytes
// (nil when it is missing) and returns the new bytes, or nil to leave the
// file alone. The file is replaced in one rename, keeping its mode (0600
// when new); a symlinked file is replaced at its target. Tools that share
// the file write it without a lock engine could take, so when check, called
// just before the rename, says the file changed since the read, the edit
// starts again, a few times at most, then fails with ErrKeptChanging. A nil
// check is FileStamp; tests replace it to play a concurrent writer.
func EditFile(path string, check func(string) (Stamp, error), edit func(data []byte) ([]byte, error)) error {
	if check == nil {
		check = stamp
	}
	if target, err := filepath.EvalSymlinks(path); err == nil {
		path = target
	}
	for range editAttempts {
		before, err := stamp(path)
		if err != nil {
			return err
		}
		var data []byte
		if before.exists {
			if data, err = os.ReadFile(path); err != nil {
				return err
			}
		}
		out, err := edit(data)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if out == nil {
			return nil
		}
		unchanged := func() bool { now, err := check(path); return err == nil && now == before }
		if done, err := replaceFile(path, []byte(strings.TrimSuffix(string(out), "\n")), before.mode, unchanged); done || err != nil {
			return err
		}
	}
	return fmt.Errorf("%s %w", path, ErrKeptChanging)
}

// WriteFile replaces the file at path with data in one rename, so a reader
// never sees half a file.
func WriteFile(path string, data []byte, mode fs.FileMode) error {
	_, err := replaceFile(path, []byte(strings.TrimSuffix(string(data), "\n")), mode, func() bool { return true })
	return err
}

// TrustKeys are the paths that name dir to a harness's folder trust: dir,
// and its resolved path when that differs.
func TrustKeys(dir string) []string {
	keys := []string{dir}
	if real, err := filepath.EvalSymlinks(dir); err == nil && real != dir {
		keys = append(keys, real)
	}
	return keys
}

// ValidName accepts a word that is safe as one path part: letters, digits,
// _, . and -, and never "." or "..".
func ValidName(s string) bool { return validPart(s) }
