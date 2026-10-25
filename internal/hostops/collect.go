package hostops

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/dinoderek/engine/internal/session"
)

// Collected is collect's reply: a run directory, as it is on this host.
type Collected struct {
	RunDir string `json:"run_dir"`
	// Run is run.json as written, or null when the run has none, as a
	// preflight check's run has not.
	Run json.RawMessage `json:"run"`
	// Files are every file in the run directory, relative to it.
	Files []string `json:"files"`
}

// Collect reads the run directory of runID. It uploads nothing.
func Collect(d Deps, runID string) (Collected, error) {
	dir, err := session.RunDir(d.Home, runID)
	if err != nil {
		return Collected{}, refuse("%v", err)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return Collected{}, refuse("no run %s on this host (%s)", runID, dir)
	}
	c := Collected{RunDir: dir, Run: json.RawMessage("null"), Files: []string{}}
	data, err := os.ReadFile(filepath.Join(dir, session.RecordFile))
	switch {
	case err == nil && json.Valid(data):
		c.Run = data
	case err == nil:
		return Collected{}, errors.New(filepath.Join(dir, session.RecordFile) + " is not valid JSON")
	case !errors.Is(err, fs.ErrNotExist):
		return Collected{}, err
	}
	err = filepath.WalkDir(dir, func(path string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		c.Files = append(c.Files, filepath.ToSlash(rel))
		return err
	})
	return c, err
}
