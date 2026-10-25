package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The notes a coordinator keeps in a run directory, each one a file holding
// an RFC 3339 time.
const (
	// WaitingSince is when the session was first seen waiting for input.
	WaitingSince = "waiting-since"
	// StallNoted is when the coordinator last raised a stall for the run.
	StallNoted = "stall-noted"
)

// Run is one run directory and its run.json.
type Run struct {
	Dir    string
	Record Record
}

// LatestRun returns the newest run on this host of the session named name:
// the run directory whose run.json names it and was spawned last.
func LatestRun(home, name string) (Run, bool, error) {
	runs, err := latestRuns(home, name+"-*")
	r, ok := runs[name]
	return r, ok, err
}

// LatestRuns returns the newest run on this host of every session name, by
// name.
func LatestRuns(home string) (map[string]Run, error) { return latestRuns(home, "*") }

// latestRuns reads the run.json of every run directory matching pattern, and
// keeps the newest run of each session name.
func latestRuns(home, pattern string) (map[string]Run, error) {
	if home == "" {
		return nil, errors.New("no home directory to read run records from")
	}
	paths, err := filepath.Glob(filepath.Join(home, filepath.FromSlash(RunsDir), pattern, RecordFile))
	if err != nil {
		return nil, err
	}
	latest := map[string]Run{}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var rec Record
		if err := json.Unmarshal(data, &rec); err != nil {
			return nil, err
		}
		if prev, ok := latest[rec.Name]; ok && !rec.Spawned.After(prev.Record.Spawned) {
			continue
		}
		latest[rec.Name] = Run{Dir: filepath.Dir(path), Record: rec}
	}
	return latest, nil
}

// ReadNote returns the time a note holds, and whether it is there.
func ReadNote(dir, note string) (time.Time, bool, error) {
	data, err := os.ReadFile(filepath.Join(dir, note))
	if errors.Is(err, os.ErrNotExist) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(data)))
	return t, err == nil, err
}

// WriteNote sets a note to at.
func WriteNote(dir, note string, at time.Time) error {
	return os.WriteFile(filepath.Join(dir, note), []byte(at.UTC().Format(time.RFC3339)+"\n"), 0o644)
}

// ClearNote removes a note; a missing one is not an error.
func ClearNote(dir, note string) error {
	err := os.Remove(filepath.Join(dir, note))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
