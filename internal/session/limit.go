package session

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// StateLimited is a live session stopped on a harness usage limit.
const StateLimited = "limited"

// LimitFile is Claude's current limit marker in a run directory.
const LimitFile = "limit.json"

// Limit records a usage stop; a zero reset means the reset is unknown.
type Limit struct {
	Since        int64  `json:"since"`
	ResetsAt     int64  `json:"resets_at"`
	Error        string `json:"error"`
	Notification string `json:"notification_type,omitempty"`
}

// ReadLimit reads a run's limit marker. Missing or malformed records are
// unknown; reset times remain after expiry until a resume clears the marker.
func ReadLimit(dir string) (Limit, bool) {
	var l Limit
	data, err := os.ReadFile(filepath.Join(dir, LimitFile))
	if err != nil || json.Unmarshal(data, &l) != nil || l.Since <= 0 || l.ResetsAt < 0 || l.Error == "" {
		return Limit{}, false
	}
	return l, true
}
