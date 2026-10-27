package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestQuotaMergeAndExpiration(t *testing.T) {
	older := Quota{Harness: "claude", ObservedAt: 100, Windows: map[string]QuotaWindow{"five_hour": {Used: 90, ResetsAt: 200}}}
	newest := Quota{Harness: "claude", ObservedAt: 110, Windows: map[string]QuotaWindow{"five_hour": {Used: 100, ResetsAt: 150}, "seven_day": {Used: 41, ResetsAt: 300}}}
	got := MergeQuota(time.Unix(150, 0), map[string]Quota{"claude": newest}, map[string]Quota{"claude": older})
	if got["claude"].ObservedAt != 110 || got["claude"].Windows["five_hour"].Used != 0 || got["claude"].Windows["seven_day"].Used != 41 {
		t.Fatalf("merged: %+v", got)
	}
	if newest.Windows["five_hour"].Used != 100 {
		t.Fatal("mutated source")
	}
}

func TestReadQuotaAcrossAllRuns(t *testing.T) {
	home := t.TempDir()
	put := func(run, body string) {
		t.Helper()
		dir := filepath.Join(home, RunsDir, run)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, QuotaFile), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if got := ReadQuota(home, time.Unix(150, 0)); got != nil {
		t.Fatal(got)
	}
	put("old", `{"harness":"claude","observed_at":100,"windows":{"five_hour":{"used":80,"resets_at":200}}}`)
	put("new", `{"harness":"claude","observed_at":110,"windows":{"five_hour":{"used":83,"resets_at":200}}}`)
	put("bad", `{"harness":"claude","observed_at":120,"windows":{"five_hour":{"used":200,"resets_at":200}}}`)
	put("broken", `{`)
	got := ReadQuota(home, time.Unix(150, 0))
	if len(got) != 1 || got["claude"].ObservedAt != 110 || got["claude"].Windows["five_hour"].Used != 83 {
		t.Fatalf("%+v", got)
	}
}

func TestQuotaValidation(t *testing.T) {
	for _, body := range []string{`{}`, `{"harness":"claude"}`, `{"harness":"claude","observed_at":1,"windows":{}}`, `{"harness":"claude","observed_at":1,"windows":{"bad":{"used":1,"resets_at":2}}}`, `{"harness":"claude","observed_at":1,"windows":{"five_hour":{"used":-1,"resets_at":2}}}`, `{"harness":"claude","observed_at":1,"windows":{"five_hour":{"used":1}}}`} {
		var q Quota
		if err := json.Unmarshal([]byte(body), &q); err != nil {
			t.Fatal(err)
		}
		if validQuota(q) {
			t.Errorf("accepted %s", body)
		}
	}
}
