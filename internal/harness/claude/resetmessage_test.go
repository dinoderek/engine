package claude

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestMessageResetIsTheNextTimeInTheZone(t *testing.T) {
	zone := time.FixedZone("host", -5*3600)
	// 14:10 in the host's zone.
	now := time.Date(2026, 11, 20, 14, 10, 0, 0, zone)
	for _, tc := range []struct {
		name, message string
		want          time.Time
	}{
		{"hour", "You've hit your limit · resets 3pm", time.Date(2026, 11, 20, 15, 0, 0, 0, zone)},
		{"minutes", "5-hour limit reached ∙ resets 3:30pm (America/Chicago)", time.Date(2026, 11, 20, 15, 30, 0, 0, zone)},
		{"past today is tomorrow", "limit reached, resets 9am", time.Date(2026, 11, 21, 9, 0, 0, 0, zone)},
		{"now is tomorrow", "resets 2:10pm", time.Date(2026, 11, 21, 14, 10, 0, 0, zone)},
		{"twenty-four hour", "Resets at 23:05", time.Date(2026, 11, 20, 23, 5, 0, 0, zone)},
		{"midnight", "resets 12am", time.Date(2026, 11, 21, 0, 0, 0, 0, zone)},
		{"noon spelt out", "resets 12 p.m.", time.Date(2026, 11, 21, 12, 0, 0, 0, zone)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(map[string]any{"hook_event_name": "StopFailure", "error": "rate_limit", "last_assistant_message": tc.message})
			if got := messageReset(raw, now); got != tc.want.Unix() {
				t.Errorf("reset %v, want %v", time.Unix(got, 0).In(zone), tc.want)
			}
		})
	}
}

func TestMessageResetUnknown(t *testing.T) {
	now := time.Date(2026, 11, 20, 14, 10, 0, 0, time.UTC)
	for _, message := range []string{"resets Nov 24, 5pm", "resets 3", "resets 13pm", "resets 9:75am", "resets 25:00", "usage limit reached", ""} {
		raw, _ := json.Marshal(map[string]any{"error_details": message})
		if got := messageReset(raw, now); got != 0 {
			t.Errorf("%q: reset %d, want unknown", message, got)
		}
	}
	if got := messageReset(json.RawMessage("{"), now); got != 0 {
		t.Errorf("bad input: reset %d", got)
	}
}

func TestLimitHookFallsBackToTheMessage(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 20, 14, 10, 0, 0, time.UTC)
	input := `{"hook_event_name":"StopFailure","error":"rate_limit","error_details":[{"text":"limit · resets 4pm"}]}`
	if err := recordLimit(strings.NewReader(input), dir, now); err != nil {
		t.Fatal(err)
	}
	l, ok := ReadLimit(dir)
	if want := time.Date(2026, 11, 20, 16, 0, 0, 0, time.UTC).Unix(); !ok || l.ResetsAt != want {
		t.Fatalf("marker %+v, %v; want reset %d", l, ok, want)
	}
	// The quota reading still wins over the message.
	putLimitTest(t, dir, "quota.json", `{"windows":{"five_hour":{"used":100,"resets_at":9000}}}`)
	if err := recordLimit(strings.NewReader(input), dir, now); err != nil {
		t.Fatal(err)
	}
	if l, _ := ReadLimit(dir); l.ResetsAt != 9000 {
		t.Errorf("reset %d, want the quota's", l.ResetsAt)
	}
}
