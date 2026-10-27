package hook

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

type limitRecord struct {
	Since        int64  `json:"since"`
	ResetsAt     int64  `json:"resets_at"`
	Error        string `json:"error"`
	Notification string `json:"notification_type,omitempty"`
}

type limitEvent struct {
	Event        string          `json:"hook_event_name"`
	Error        string          `json:"error"`
	ResetsAt     json.RawMessage `json:"resets_at"`
	Details      json.RawMessage `json:"error_details"`
	Notification string          `json:"notification_type"`
}

// Limit keeps Claude's limit marker and records each stop's complete input
// in the run's telemetry. No run directory means this is not an engine run.
func Limit(in io.Reader, dir string, now time.Time) error {
	if dir == "" {
		return nil
	}
	var raw json.RawMessage
	if err := json.NewDecoder(in).Decode(&raw); err != nil {
		return err
	}
	var e limitEvent
	if err := json.Unmarshal(raw, &e); err != nil {
		return err
	}
	file := filepath.Join(dir, "limit.json")
	switch e.Event {
	case "StopFailure":
		if e.Error != "rate_limit" && e.Error != "billing_error" {
			return nil
		}
		r := limitRecord{Since: now.Unix(), ResetsAt: limitReset(e, dir), Error: e.Error}
		if err := logLimit(dir, r, raw); err != nil {
			return err
		}
		return replaceLimit(file, r)
	case "UserPromptSubmit":
		return removeLimit(file)
	case "Notification":
		return notifyLimit(file, e.Notification)
	}
	return nil
}

func limitReset(e limitEvent, dir string) int64 {
	if reset := resetTime(e.ResetsAt); reset > 0 {
		return reset
	}
	var details struct {
		ResetsAt json.RawMessage `json:"resets_at"`
	}
	if json.Unmarshal(e.Details, &details) == nil {
		if reset := resetTime(details.ResetsAt); reset > 0 {
			return reset
		}
	}
	var q quotaReading
	data, _ := os.ReadFile(filepath.Join(dir, "quota.json"))
	if json.Unmarshal(data, &q) != nil {
		return 0
	}
	var reset int64
	for _, w := range q.Windows {
		if w.Used >= 100 {
			reset = max(reset, w.ResetsAt)
		}
	}
	if reset == 0 {
		reset = q.Windows["five_hour"].ResetsAt
	}
	return reset
}

func notifyLimit(file, kind string) error {
	switch kind {
	case "quota_auto_resume_fired":
		return removeLimit(file)
	case "quota_auto_resume_stale", "quota_auto_resume_disabled":
		data, err := os.ReadFile(file)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		var r limitRecord
		if err := json.Unmarshal(data, &r); err != nil {
			return err
		}
		r.Notification = kind
		return replaceLimit(file, r)
	}
	return nil
}

func removeLimit(file string) error {
	err := os.Remove(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func replaceLimit(file string, r limitRecord) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), ".limit-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err = tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), file)
}

func logLimit(dir string, r limitRecord, raw json.RawMessage) error {
	f, err := os.OpenFile(filepath.Join(dir, "limits.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	err = json.NewEncoder(f).Encode(struct {
		limitRecord
		Input json.RawMessage `json:"input"`
	}{r, raw})
	return errors.Join(err, f.Close())
}

// resetTime accepts epoch seconds or an explicit timestamp. An unfamiliar
// reset value leaves the quota fallback available and the raw input logged.
func resetTime(raw json.RawMessage) int64 {
	var seconds int64
	if json.Unmarshal(raw, &seconds) == nil {
		return max(seconds, 0)
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return 0
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		return max(seconds, 0)
	}
	if at, err := time.Parse(time.RFC3339, value); err == nil {
		return max(at.Unix(), 0)
	}
	return 0
}
