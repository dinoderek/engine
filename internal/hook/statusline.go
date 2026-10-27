package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"time"
)

type usageWindow struct {
	Used     *float64 `json:"used_percentage"`
	ResetsAt int64    `json:"resets_at"`
}
type quotaWindow struct {
	Used     float64 `json:"used"`
	ResetsAt int64   `json:"resets_at"`
}
type quotaReading struct {
	Harness    string                 `json:"harness"`
	ObservedAt int64                  `json:"observed_at"`
	Windows    map[string]quotaWindow `json:"windows"`
}

// Statusline prints the engine session name and Claude's five-hour usage,
// and atomically records changed readings. Absent quota never creates a file.
func Statusline(in io.Reader, out io.Writer, dir string, now time.Time) error {
	var input struct {
		Name   string                  `json:"session_name"`
		Limits map[string]*usageWindow `json:"rate_limits"`
	}
	if err := json.NewDecoder(in).Decode(&input); err != nil {
		return err
	}
	name := input.Name
	if dir != "" {
		var rec struct {
			Name string `json:"name"`
		}
		data, _ := os.ReadFile(filepath.Join(dir, "run.json"))
		if json.Unmarshal(data, &rec) == nil && rec.Name != "" {
			name = rec.Name
		}
	}
	q := quotaReading{Harness: "claude", ObservedAt: now.Unix(), Windows: map[string]quotaWindow{}}
	for _, key := range []string{"five_hour", "seven_day"} {
		w := input.Limits[key]
		if w == nil || w.Used == nil || *w.Used < 0 || *w.Used > 100 || w.ResetsAt <= 0 {
			continue
		}
		q.Windows[key] = quotaWindow{Used: *w.Used, ResetsAt: w.ResetsAt}
	}
	line := name
	if w, ok := q.Windows["five_hour"]; ok {
		used := w.Used
		if now.Unix() >= w.ResetsAt {
			used = 0
		}
		line += fmt.Sprintf("  5h %g%%", used)
	}
	if _, err := fmt.Fprintln(out, line); err != nil {
		return err
	}
	if dir == "" || len(q.Windows) == 0 {
		return nil
	}
	return writeQuota(dir, q)
}

func writeQuota(dir string, q quotaReading) error {
	file := filepath.Join(dir, "quota.json")
	var old quotaReading
	data, _ := os.ReadFile(file)
	if json.Unmarshal(data, &old) == nil && old.Harness == q.Harness && reflect.DeepEqual(old.Windows, q.Windows) {
		return nil
	}
	data, err := json.Marshal(q)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".quota-*")
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
