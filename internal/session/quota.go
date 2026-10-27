package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// QuotaFile holds a harness's latest subscription usage in a run directory.
const QuotaFile = "quota.json"

// Quota is a harness account's reading. Times are epoch seconds.
type Quota struct {
	Harness    string                 `json:"harness"`
	ObservedAt int64                  `json:"observed_at"`
	Windows    map[string]QuotaWindow `json:"windows"`
}

// QuotaWindow is the percentage used and the time the window resets.
type QuotaWindow struct {
	Used     float64 `json:"used"`
	ResetsAt int64   `json:"resets_at"`
}

// MergeQuota keeps the newest observation per harness, then clears expired
// usage in copies. It never changes the source readings or their timestamps.
func MergeQuota(now time.Time, readings ...map[string]Quota) map[string]Quota {
	out := map[string]Quota{}
	for _, group := range readings {
		for _, q := range group {
			if q.ObservedAt <= out[q.Harness].ObservedAt {
				continue
			}
			copy := q
			copy.Windows = map[string]QuotaWindow{}
			for name, w := range q.Windows {
				if now.Unix() >= w.ResetsAt {
					w.Used = 0
				}
				copy.Windows[name] = w
			}
			out[q.Harness] = copy
		}
	}
	return out
}

// ReadQuota scans all runs, including ended sessions and older runs of a
// session. Missing or malformed readings are unknown and are ignored.
func ReadQuota(home string, now time.Time) map[string]Quota {
	files, _ := filepath.Glob(filepath.Join(home, filepath.FromSlash(RunsDir), "*", QuotaFile))
	out := map[string]Quota{}
	for _, file := range files {
		var q Quota
		data, err := os.ReadFile(file)
		if err != nil || json.Unmarshal(data, &q) != nil || !validQuota(q) {
			continue
		}
		out = MergeQuota(now, out, map[string]Quota{q.Harness: q})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func validQuota(q Quota) bool {
	if q.Harness == "" || q.ObservedAt <= 0 || len(q.Windows) == 0 {
		return false
	}
	for name, w := range q.Windows {
		if (name != "five_hour" && name != "seven_day") || w.Used < 0 || w.Used > 100 || w.ResetsAt <= 0 {
			return false
		}
	}
	return true
}
