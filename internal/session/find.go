package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// ReadRecord reads run.json from a run directory.
func ReadRecord(dir string) (Record, error) {
	var rec Record
	data, err := os.ReadFile(filepath.Join(dir, RecordFile))
	if err != nil {
		return rec, err
	}
	return rec, json.Unmarshal(data, &rec)
}

// FindRun finds the run, under home, whose worktree holds cwd: the worktree
// itself or a folder below it. Of the worktrees that hold it the deepest
// wins, and of the runs of one worktree the newest. It returns the run's
// directory and record, or false when cwd is in no run's worktree. A run
// directory with no readable run.json is skipped.
func FindRun(home, cwd string) (string, Record, bool) {
	if home == "" || cwd == "" {
		return "", Record{}, false
	}
	runs := filepath.Join(home, filepath.FromSlash(RunsDir))
	entries, err := os.ReadDir(runs)
	if err != nil {
		return "", Record{}, false
	}
	where := resolved(cwd)
	var best Record
	var bestDir string
	bestDepth := -1
	for _, e := range entries {
		dir := filepath.Join(runs, e.Name())
		rec, err := ReadRecord(dir)
		if err != nil || rec.Worktree == "" {
			continue
		}
		worktree := resolved(rec.Worktree)
		if !within(worktree, where) {
			continue
		}
		depth := len(worktree)
		if depth > bestDepth || (depth == bestDepth && rec.Spawned.After(best.Spawned)) {
			best, bestDir, bestDepth = rec, dir, depth
		}
	}
	return bestDir, best, bestDir != ""
}

// resolved is path cleaned and, when it exists, with its symlinks followed,
// so a hook's cwd and a recorded worktree compare equal on a host whose
// temporary or home directories are links.
func resolved(path string) string {
	path = filepath.Clean(path)
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return path
}

// within reports whether path is dir or below it.
func within(dir, path string) bool {
	return path == dir || strings.HasPrefix(path, strings.TrimSuffix(dir, string(filepath.Separator))+string(filepath.Separator))
}
