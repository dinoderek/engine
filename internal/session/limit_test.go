package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadLimit(t *testing.T) {
	for _, tc := range []struct {
		body string
		ok   bool
	}{
		{"", false}, {"{", false}, {`{}`, false},
		{`{"since":1000,"resets_at":-1,"error":"rate_limit"}`, false},
		{`{"since":1000,"resets_at":2000,"error":"rate_limit"}`, true},
		{`{"since":1000,"resets_at":0,"error":"billing_error","notification_type":"quota_auto_resume_stale"}`, true},
	} {
		dir := t.TempDir()
		if tc.body != "" {
			if err := os.WriteFile(filepath.Join(dir, LimitFile), []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		l, ok := ReadLimit(dir)
		if ok != tc.ok {
			t.Errorf("%s: %+v, %v", tc.body, l, ok)
		}
	}
}
