package claude

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/dinoderek/engine/internal/harness"
)

// LimitFile is Claude's current limit marker in a run directory, which
// engine's limit hook writes on a usage stop and clears on a resume.
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

// RunLimit is the run's limit marker, for status to read when its window is
// gone.
func (Adapter) RunLimit(dir string) (int64, bool) {
	l, ok := ReadLimit(dir)
	return l.ResetsAt, ok
}

var _ harness.LimitMarker = Adapter{}
