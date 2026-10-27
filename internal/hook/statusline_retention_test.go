package hook

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestStatuslineRetainsLastValidQuota(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		malformed   bool
	}{
		{"absent limits", `{"session_name":"test"}`, false},
		{"null limits", `{"session_name":"test","rate_limits":null}`, false},
		{"empty limits", `{"session_name":"test","rate_limits":{}}`, false},
		{"null windows", `{"session_name":"test","rate_limits":{"five_hour":null,"seven_day":null}}`, false},
		{"empty windows", `{"session_name":"test","rate_limits":{"five_hour":{},"seven_day":{}}}`, false},
		{"null percentages", `{"session_name":"test","rate_limits":{"five_hour":{"used_percentage":null,"resets_at":2000},"seven_day":{"resets_at":3000}}}`, false},
		{"invalid percentages", `{"session_name":"test","rate_limits":{"five_hour":{"used_percentage":-1,"resets_at":2000},"seven_day":{"used_percentage":101,"resets_at":3000}}}`, false},
		{"invalid resets", `{"session_name":"test","rate_limits":{"five_hour":{"used_percentage":83,"resets_at":0},"seven_day":{"used_percentage":41,"resets_at":-1}}}`, false},
		{"missing resets", `{"session_name":"test","rate_limits":{"five_hour":{"used_percentage":83},"seven_day":{"used_percentage":41,"resets_at":null}}}`, false},
		{"malformed JSON", `{"session_name":"test","rate_limits":`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			initial := `{"session_name":"test","rate_limits":{"five_hour":{"used_percentage":83,"resets_at":2000},"seven_day":{"used_percentage":41,"resets_at":3000}}}`
			var out bytes.Buffer
			if err := Statusline(strings.NewReader(initial), &out, dir, time.Unix(1000, 0)); err != nil {
				t.Fatal(err)
			}
			want := quotaReading{Harness: "claude", ObservedAt: 1000, Windows: map[string]quotaWindow{
				"five_hour": {Used: 83, ResetsAt: 2000},
				"seven_day": {Used: 41, ResetsAt: 3000},
			}}
			original := assertRetainedQuota(t, dir, want)
			out.Reset()
			err := Statusline(strings.NewReader(tc.input), &out, dir, time.Unix(1100, 0))
			assertUnavailableOutput(t, out.String(), err, tc.malformed)
			retained := assertRetainedQuota(t, dir, want)
			if !bytes.Equal(original, retained) {
				t.Fatalf("unavailable quota rewrote saved bytes: %s", retained)
			}
			changed := strings.Replace(initial, "83", "84", 1)
			out.Reset()
			if err := Statusline(strings.NewReader(changed), &out, dir, time.Unix(1200, 0)); err != nil {
				t.Fatal(err)
			}
			if out.String() != "test  5h 84%\n" {
				t.Fatalf("changed quota output = %q", out.String())
			}
			want.ObservedAt = 1200
			want.Windows["five_hour"] = quotaWindow{Used: 84, ResetsAt: 2000}
			replacement := assertRetainedQuota(t, dir, want)
			if bytes.Equal(original, replacement) {
				t.Fatal("changed quota did not replace saved bytes")
			}
		})
	}
}

func assertRetainedQuota(t *testing.T, dir string, want quotaReading) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "quota.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got quotaReading
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("saved quota = %+v, want %+v", got, want)
	}
	return data
}

func assertUnavailableOutput(t *testing.T, output string, err error, malformed bool) {
	t.Helper()
	if malformed {
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("malformed JSON error = %v, want unexpected EOF", err)
		}
		if output != "" {
			t.Fatalf("malformed JSON output = %q, want empty", output)
		}
		return
	}
	if err != nil || output != "test\n" {
		t.Fatalf("unavailable quota output = %q, error = %v", output, err)
	}
}
