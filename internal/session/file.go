package session

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// fileStamp tells whether a file changed: a missing file has the zero stamp.
type fileStamp struct {
	exists bool
	mod    int64
	size   int64
	mode   fs.FileMode
}

func stamp(path string) (fileStamp, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return fileStamp{mode: 0o600}, nil
	}
	if err != nil {
		return fileStamp{}, err
	}
	return fileStamp{exists: true, mod: info.ModTime().UnixNano(), size: info.Size(), mode: info.Mode().Perm()}, nil
}

// replaceFile writes data beside path and renames it over path, so a reader
// never sees half a file. It renames only if unchanged still holds just
// before, and reports whether it did.
func replaceFile(path string, data []byte, mode fs.FileMode, unchanged func() bool) (bool, error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".engine-*")
	if err != nil {
		return false, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // a no-op once renamed
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return false, err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if !unchanged() {
		return false, nil
	}
	return true, os.Rename(tmp.Name(), path)
}
