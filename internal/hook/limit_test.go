package hook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/session"
)

func limitInput(kind string) string {
	return `{"hook_event_name":"StopFailure","error":"` + kind + `","error_details":"subscription stop","extra":"retained"}`
}

func TestLimitStopsAndResetFallbacks(t *testing.T) {
	for _, tc := range []struct {
		name, input, quota string
		reset              int64
	}{
		{"direct wins", `{"hook_event_name":"StopFailure","error":"rate_limit","resets_at":4000}`, `{"windows":{"five_hour":{"used":100,"resets_at":2000}}}`, 4000},
		{"text timestamp", `{"hook_event_name":"StopFailure","error":"rate_limit","resets_at":"1970-01-01T00:50:00Z"}`, "", 3000},
		{"text seconds", `{"hook_event_name":"StopFailure","error":"rate_limit","resets_at":"3000"}`, "", 3000},
		{"unfamiliar reset", `{"hook_event_name":"StopFailure","error":"rate_limit","resets_at":{"unknown":true}}`, `{"windows":{"five_hour":{"used":100,"resets_at":2000}}}`, 2000},
		{"bad reset text", `{"hook_event_name":"StopFailure","error":"rate_limit","resets_at":"unknown"}`, "", 0},
		{"negative reset", `{"hook_event_name":"StopFailure","error":"rate_limit","resets_at":-1}`, "", 0},
		{"details reset", `{"hook_event_name":"StopFailure","error":"billing_error","error_details":{"resets_at":3000}}`, "", 3000},
		{"latest exhausted", limitInput("rate_limit"), `{"windows":{"five_hour":{"used":100,"resets_at":2000},"seven_day":{"used":100,"resets_at":5000}}}`, 5000},
		{"only exhausted", limitInput("billing_error"), `{"windows":{"five_hour":{"used":100,"resets_at":2000},"seven_day":{"used":50,"resets_at":5000}}}`, 2000},
		{"five hour fallback", limitInput("rate_limit"), `{"windows":{"five_hour":{"used":99,"resets_at":2000},"seven_day":{"used":30,"resets_at":5000}}}`, 2000},
		{"absent quota", limitInput("billing_error"), "", 0},
		{"corrupt quota", limitInput("rate_limit"), "{", 0},
		{"partial quota", limitInput("rate_limit"), `{"windows":{"seven_day":{"used":99,"resets_at":5000}}}`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.quota != "" {
				putLimitTest(t, dir, "quota.json", tc.quota)
			}
			for range 2 {
				if err := Limit(strings.NewReader(tc.input), dir, time.Unix(1000, 0)); err != nil {
					t.Fatal(err)
				}
			}
			l, ok := session.ReadLimit(dir)
			if !ok || l.Since != 1000 || l.ResetsAt != tc.reset || l.Error == "" {
				t.Fatalf("marker %+v, %v", l, ok)
			}
			checkLimitTelemetry(t, dir, tc.input, tc.reset)
			files, _ := filepath.Glob(filepath.Join(dir, ".limit-*"))
			if len(files) != 0 {
				t.Errorf("temporary files %v", files)
			}
		})
	}
}

func putLimitTest(t *testing.T, dir, file, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLimitEventsKeepOrClear(t *testing.T) {
	for _, tc := range []struct {
		event, kind string
		keep        bool
	}{
		{"Notification", "quota_auto_resume_stale", true},
		{"Notification", "quota_auto_resume_disabled", true},
		{"Notification", "quota_auto_resume_fired", false},
		{"UserPromptSubmit", "", false},
		{"Notification", "idle_prompt", true},
		{"Stop", "", true},
	} {
		t.Run(tc.event+tc.kind, func(t *testing.T) {
			dir := t.TempDir()
			if err := Limit(strings.NewReader(limitInput("rate_limit")), dir, time.Unix(1000, 0)); err != nil {
				t.Fatal(err)
			}
			input := `{"hook_event_name":"` + tc.event + `","notification_type":"` + tc.kind + `"}`
			if err := Limit(strings.NewReader(input), dir, time.Unix(2000, 0)); err != nil {
				t.Fatal(err)
			}
			l, ok := session.ReadLimit(dir)
			if ok != tc.keep {
				t.Fatalf("marker %+v, %v", l, ok)
			}
			if tc.keep && l.Since != 1000 {
				t.Errorf("stop changed: %+v", l)
			}
			if strings.HasPrefix(tc.kind, "quota_auto_resume_") && tc.keep && l.Notification != tc.kind {
				t.Errorf("notification %+v", l)
			}
			data, _ := os.ReadFile(filepath.Join(dir, "limits.jsonl"))
			if len(strings.Split(strings.TrimSpace(string(data)), "\n")) != 1 {
				t.Errorf("history %s", data)
			}
		})
	}
}

func TestLimitOutsideRunsAndFailures(t *testing.T) {
	if err := Limit(strings.NewReader("{"), "", time.Time{}); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{`{"hook_event_name":"StopFailure","error":"overloaded"}`, `{"hook_event_name":"Notification","notification_type":"quota_auto_resume_stale"}`, `{"hook_event_name":"UserPromptSubmit"}`} {
		dir := t.TempDir()
		if err := Limit(strings.NewReader(input), dir, time.Unix(1000, 0)); err != nil {
			t.Fatal(err)
		}
		files, _ := os.ReadDir(dir)
		if len(files) != 0 {
			t.Errorf("files %v", files)
		}
	}
	for _, tc := range []struct {
		name, input string
		setup       func(string)
	}{
		{"bad json", "{", nil},
		{"bad event", `{"hook_event_name":42}`, nil},
		{"missing directory", limitInput("rate_limit"), func(dir string) { _ = os.Remove(dir) }},
		{"log blocked", limitInput("rate_limit"), func(dir string) { _ = os.Mkdir(filepath.Join(dir, "limits.jsonl"), 0o700) }},
		{"marker blocked", limitInput("rate_limit"), func(dir string) { _ = os.Mkdir(filepath.Join(dir, "limit.json"), 0o700) }},
		{"bad marker", `{"hook_event_name":"Notification","notification_type":"quota_auto_resume_disabled"}`, func(dir string) { putLimitTest(t, dir, "limit.json", "{") }},
		{"unreadable marker", `{"hook_event_name":"Notification","notification_type":"quota_auto_resume_stale"}`, func(dir string) { _ = os.Mkdir(filepath.Join(dir, "limit.json"), 0o700) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.setup != nil {
				tc.setup(dir)
			}
			if err := Limit(strings.NewReader(tc.input), dir, time.Unix(1000, 0)); err == nil {
				t.Error("succeeded")
			}
		})
	}
}

func checkLimitTelemetry(t *testing.T, dir, input string, reset int64) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "limits.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("stops: %s", data)
	}
	var entry struct {
		Since    int64          `json:"since"`
		ResetsAt int64          `json:"resets_at"`
		Input    map[string]any `json:"input"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatal(err)
	}
	if entry.Since != 1000 || entry.ResetsAt != reset {
		t.Errorf("entry %+v", entry)
	}
	var want map[string]any
	if err := json.Unmarshal([]byte(input), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(entry.Input, want) {
		t.Errorf("input %v, want %v", entry.Input, want)
	}
}
