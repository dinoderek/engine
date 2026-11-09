package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
// name. A run.json it cannot read is left out and named in the error, so one
// bad file hides only its own run.
func LatestRuns(home string) (map[string]Run, error) { return latestRuns(home, "*") }

// SlotsHeld counts the windows that take one of their host's slots, and how
// many of those are heavy, by their newest run. A window takes a slot when
// its command is still running and runs holds a run under its name. A window
// with no run, such as engine run's own or a stray shell, is not a session.
func SlotsHeld(windows []WindowInfo, runs map[string]Run) (held, heavy int) {
	for _, w := range windows {
		r, ok := runs[w.Name]
		if !ok || w.Dead {
			continue
		}
		held++
		if r.Record.Heavy {
			heavy++
		}
	}
	return held, heavy
}

// latestRuns reads the run.json of every run directory matching pattern, and
// keeps the newest run of each session name. The runs it could read come
// back with the error of those it could not.
func latestRuns(home, pattern string) (map[string]Run, error) {
	if home == "" {
		return nil, errors.New("no home directory to read run records from")
	}
	paths, err := filepath.Glob(filepath.Join(home, filepath.FromSlash(RunsDir), pattern, RecordFile))
	if err != nil {
		return nil, err
	}
	latest := map[string]Run{}
	var bad []error
	for _, path := range paths {
		var rec Record
		data, err := os.ReadFile(path)
		if err == nil {
			err = json.Unmarshal(data, &rec)
		}
		if err != nil {
			bad = append(bad, fmt.Errorf("%s: %w", path, err))
			continue
		}
		if prev, ok := latest[rec.Name]; ok && !rec.Spawned.After(prev.Record.Spawned) {
			continue
		}
		latest[rec.Name] = Run{Dir: filepath.Dir(path), Record: rec}
	}
	return latest, errors.Join(bad...)
}
