package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// RunsDir is where this host keeps run directories, relative to the home
// directory: one per run, at <run-id> below it, holding run.json and the
// pane log.
const RunsDir = ".local/share/engine/runs"

// RecordFile and PaneLog are the files in a run directory.
const (
	RecordFile = "run.json"
	PaneLog    = "pane.log"
)

// RunID names one run of a task: <repo>-<task>-<UTC yyyymmddThhmmssZ>.
func RunID(repo Repo, task int, at time.Time) string {
	return TaskName(repo, task) + "-" + at.UTC().Format("20060102T150405Z")
}

// RunDir is the directory of the run under home.
func RunDir(home, runID string) (string, error) {
	if home == "" {
		return "", errors.New("no home directory to keep run records in")
	}
	if !validPart(runID) {
		return "", fmt.Errorf("run id %q: use letters, digits, _, . and -", runID)
	}
	return filepath.Join(home, filepath.FromSlash(RunsDir), runID), nil
}

// RecordSchema is the version of run.json's layout.
const RecordSchema = 1

// Record is run.json as it is written at spawn. A field the run learns later,
// such as the harness's own session ID, is added when it is known.
type Record struct {
	Schema int    `json:"schema"`
	RunID  string `json:"run_id"`
	// Repo is owner/name; Task and Feature are owner/repo#n.
	Repo    string `json:"repo"`
	Task    string `json:"task"`
	Feature string `json:"feature,omitempty"`
	Host    string `json:"host"`
	Harness string `json:"harness"`
	Tier    string `json:"tier"`
	Model   string `json:"model"`
	Effort  string `json:"effort"`
	Mode    string `json:"mode"`
	// Name is the session name: the tmux window and the Remote Control name.
	Name     string    `json:"name"`
	Tmux     string    `json:"tmux"` // the window's target, such as engine:@3
	Branch   string    `json:"branch"`
	Worktree string    `json:"worktree"`
	PaneLog  string    `json:"pane_log"`
	Spawned  time.Time `json:"spawned"`
}

// WriteRecord writes rec as run.json in dir, creating dir.
func WriteRecord(dir string, rec Record) error {
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, RecordFile), append(data, '\n'), 0o644)
}
