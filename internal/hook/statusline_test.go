package hook

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStatusline(t *testing.T) {
	for _, tc := range []struct {
		name, limits, line string
		count              int
	}{
		{"full", `{"five_hour":{"used_percentage":83,"resets_at":2000},"seven_day":{"used_percentage":41,"resets_at":3000}}`, "engine-test  5h 83%\n", 2},
		{"partial", `{"seven_day":{"used_percentage":41,"resets_at":3000}}`, "engine-test\n", 1},
		{"absent", `null`, "engine-test\n", 0},
		{"empty", `{}`, "engine-test\n", 0},
		{"null window", `{"five_hour":null}`, "engine-test\n", 0},
		{"invalid", `{"five_hour":{"used_percentage":101,"resets_at":2000},"seven_day":{"used_percentage":41}}`, "engine-test\n", 0},
		{"expired", `{"five_hour":{"used_percentage":100,"resets_at":1000}}`, "engine-test  5h 0%\n", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "run.json"), []byte(`{"name":"engine-test"}`), 0600); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err := Statusline(strings.NewReader(`{"session_name":"other","rate_limits":`+tc.limits+`}`), &out, dir, time.Unix(1000, 0)); err != nil {
				t.Fatal(err)
			}
			if out.String() != tc.line {
				t.Errorf("line %q, want %q", out.String(), tc.line)
			}
			data, err := os.ReadFile(filepath.Join(dir, "quota.json"))
			if tc.count == 0 {
				if !os.IsNotExist(err) {
					t.Fatalf("unexpected quota: %s, %v", data, err)
				}
				return
			}
			var q quotaReading
			if err != nil || json.Unmarshal(data, &q) != nil || len(q.Windows) != tc.count || q.Harness != "claude" || q.ObservedAt != 1000 {
				t.Fatalf("quota %s, %v", data, err)
			}
		})
	}
}

func TestStatuslineWritesOnlyChangedNumbersAndRenames(t *testing.T) {
	dir := t.TempDir()
	input := `{"session_name":"test","rate_limits":{"five_hour":{"used_percentage":83,"resets_at":2000}}}`
	run := func(in string, at int64) {
		t.Helper()
		if err := Statusline(strings.NewReader(in), io.Discard, dir, time.Unix(at, 0)); err != nil {
			t.Fatal(err)
		}
	}
	run(input, 1000)
	file := filepath.Join(dir, "quota.json")
	first, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	old, err := io.ReadAll(first)
	if err != nil {
		t.Fatal(err)
	}
	run(input, 1001)
	same, _ := os.ReadFile(file)
	if !bytes.Equal(old, same) {
		t.Fatal("unchanged reading was rewritten")
	}
	run(strings.Replace(input, "83", "84", 1), 1002)
	changed, _ := os.ReadFile(file)
	if bytes.Equal(old, changed) || !bytes.Contains(changed, []byte(`"observed_at":1002`)) {
		t.Fatalf("unchanged file: %s", changed)
	}
	// A reader holding the old inode still sees the entire old file after rename.
	if _, err := first.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	held, _ := io.ReadAll(first)
	if !bytes.Equal(old, held) {
		t.Fatal("file was overwritten in place")
	}
	run(strings.Replace(input, "2000", "2100", 1), 1003)
	changed, _ = os.ReadFile(file)
	if !bytes.Contains(changed, []byte(`"resets_at":2100`)) {
		t.Fatalf("reset change not written: %s", changed)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temporary files left: %v", entries)
	}
}

func TestStatuslineErrorsAndNoRun(t *testing.T) {
	if err := Statusline(strings.NewReader(`{`), io.Discard, "", time.Time{}); err == nil {
		t.Fatal("accepted malformed input")
	}
	var out bytes.Buffer
	if err := Statusline(strings.NewReader(`{"session_name":"hand"}`), &out, "", time.Time{}); err != nil || out.String() != "hand\n" {
		t.Fatalf("%s, %v", out.String(), err)
	}
	input := `{"rate_limits":{"five_hour":{"used_percentage":1,"resets_at":2000}}}`
	if err := Statusline(strings.NewReader(input), io.Discard, filepath.Join(t.TempDir(), "missing"), time.Unix(1000, 0)); err == nil {
		t.Fatal("ignored write failure")
	}
}
