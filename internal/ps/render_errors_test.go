package ps

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/session"
)

var errSentinel = errors.New("sentinel writer failure")

// failingWriter fails only its failAt-th write, with errSentinel, and accepts
// every other, so an error that code drops cannot resurface on a later write.
// It keeps what it accepted.
type failingWriter struct {
	failAt int
	calls  int
	out    bytes.Buffer
}

func (w *failingWriter) Write(p []byte) (int, error) {
	w.calls++
	if w.calls == w.failAt {
		return 0, errSentinel
	}
	return w.out.Write(p)
}

func quotaOf(harness string) session.Quota {
	return session.Quota{Harness: harness, ObservedAt: 100, Windows: map[string]session.QuotaWindow{
		"five_hour": {Used: 12, ResetsAt: 1000}, "seven_day": {Used: 34, ResetsAt: 2000},
	}}
}

func claimedRows() []Row {
	return []Row{
		{Task: "first-task", State: Working, Host: "mac", Harness: "claude", AgeSeconds: 3600},
		{Task: "second-task", State: "in-review", AgeSeconds: 60},
	}
}

func TestWriteReturnsWriterError(t *testing.T) {
	oneQuota := map[string]session.Quota{"claude": quotaOf("claude")}
	twoQuotas := map[string]session.Quota{"claude": quotaOf("claude"), "codex": quotaOf("codex")}
	for _, tc := range []struct {
		name    string
		write   func(*failingWriter, Listing) error
		listing Listing
		failAt  int
		// accepted is what must have reached the writer before the failure.
		accepted []string
	}{
		{"json", func(w *failingWriter, l Listing) error { return WriteJSON(w, l) }, Listing{Rows: claimedRows()}, 1, nil},
		{"json with quota", func(w *failingWriter, l Listing) error { return WriteJSON(w, l) }, Listing{Quota: oneQuota}, 1, nil},
		{"table quota line", func(w *failingWriter, l Listing) error { return WriteTable(w, l) }, Listing{Quota: oneQuota, Rows: claimedRows()}, 1, nil},
		{"table second quota line", func(w *failingWriter, l Listing) error { return WriteTable(w, l) }, Listing{Quota: twoQuotas, Rows: claimedRows()}, 2, []string{"claude  5h 12%"}},
		{"table no claims", func(w *failingWriter, l Listing) error { return WriteTable(w, l) }, Listing{}, 1, nil},
		{"table no claims after quota", func(w *failingWriter, l Listing) error { return WriteTable(w, l) }, Listing{Quota: oneQuota}, 2, []string{"claude  5h 12%"}},
		{"table row flush", func(w *failingWriter, l Listing) error { return WriteTable(w, l) }, Listing{Rows: claimedRows()}, 1, nil},
		{"table row flush after quota", func(w *failingWriter, l Listing) error { return WriteTable(w, l) }, Listing{Quota: oneQuota, Rows: claimedRows()}, 2, []string{"claude  5h 12%"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &failingWriter{failAt: tc.failAt}
			err := tc.write(w, tc.listing)
			if !errors.Is(err, errSentinel) {
				t.Fatalf("error = %v, want the writer's sentinel", err)
			}
			got := w.out.String()
			if len(tc.accepted) == 0 && got != "" {
				t.Errorf("wrote %q before the failure, want nothing", got)
			}
			for _, want := range tc.accepted {
				if !strings.Contains(got, want) {
					t.Errorf("accepted output %q lacks %q", got, want)
				}
			}
			// Rows sit in the tabwriter's buffer until the flush, so a failure
			// at or before the flush leaves no row in the output.
			if strings.Contains(got, "TASK") || strings.Contains(got, "no claimed tasks") {
				t.Errorf("output past the failing write: %q", got)
			}
		})
	}
}

func TestWriteSucceedsOnHealthyWriter(t *testing.T) {
	oneQuota := map[string]session.Quota{"claude": quotaOf("claude")}
	for _, tc := range []struct {
		name  string
		write func(*bytes.Buffer, Listing) error
		l     Listing
		want  []string
	}{
		{"json", func(b *bytes.Buffer, l Listing) error { return WriteJSON(b, l) }, Listing{Quota: oneQuota, Rows: claimedRows()}, []string{`"quota"`, `"second-task"`}},
		{"table quota and rows", func(b *bytes.Buffer, l Listing) error { return WriteTable(b, l) }, Listing{Quota: oneQuota, Rows: claimedRows()}, []string{"claude  5h 12%", "TASK", "first-task"}},
		{"table quota without claims", func(b *bytes.Buffer, l Listing) error { return WriteTable(b, l) }, Listing{Quota: oneQuota}, []string{"claude  5h 12%", "no claimed tasks"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := tc.write(&out, tc.l); err != nil {
				t.Fatalf("healthy writer failed: %v", err)
			}
			for _, want := range tc.want {
				if !strings.Contains(out.String(), want) {
					t.Errorf("output %q lacks %q", out.String(), want)
				}
			}
		})
	}
}
