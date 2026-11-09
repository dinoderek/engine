package hostops

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

// autoReview is the decision the permission hook logs for a request it left
// to the harness's own review (hook.AutoReview; hostops cannot import hook).
const autoReview = "left to auto-review"

// permissionEntry is the part of a permissions.jsonl line hostops reads.
type permissionEntry struct {
	Time                          time.Time
	Event, Tool, Decision, Reason string
	Input                         json.RawMessage
}

// Denial is a permission request the harness or the permission hook
// denied: when, the tool, its exact input, and why, when the log says.
type Denial struct {
	At       time.Time       `json:"at"`
	Tool     string          `json:"tool"`
	Input    json.RawMessage `json:"input,omitempty"`
	Decision string          `json:"decision"`
	Reason   string          `json:"reason,omitempty"`
}

// readPermissionLog reads permissions.jsonl, if any, one entry per line.
func readPermissionLog(path string) ([]permissionEntry, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var out []permissionEntry
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scan.Scan() {
		var e permissionEntry
		if err := json.Unmarshal(scan.Bytes(), &e); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		out = append(out, e)
	}
	return out, scan.Err()
}

// readPermissions reads permissions.jsonl, if any, as one line per entry:
// the denials, and apart from them the requests left to auto-review.
func readPermissions(path string) (denied, reviewed []string, err error) {
	entries, err := readPermissionLog(path)
	if err != nil {
		return nil, nil, err
	}
	for _, e := range entries {
		line := fmt.Sprintf("%s %s: %s", e.Tool, e.Input, e.Decision)
		if e.Reason != "" {
			line += " (" + e.Reason + ")"
		}
		if e.Decision == autoReview {
			reviewed = append(reviewed, line)
		} else {
			denied = append(denied, line)
		}
	}
	return denied, reviewed, nil
}

// LastDenial is the newest entry of permissions.jsonl at path when that
// entry is a denial, and nil when it is not, or the log is missing or
// unreadable: a request left to auto-review denies nothing.
func LastDenial(path string) *Denial {
	entries, err := readPermissionLog(path)
	if err != nil || len(entries) == 0 {
		return nil
	}
	e := entries[len(entries)-1]
	if e.Decision == autoReview || e.Decision == "" {
		return nil
	}
	return &Denial{At: e.Time, Tool: e.Tool, Input: e.Input, Decision: e.Decision, Reason: e.Reason}
}
