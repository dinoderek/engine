package ps

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/tracker"
)

func TestAge(t *testing.T) {
	cases := map[time.Duration]string{
		0: "<1m", 59 * time.Second: "<1m", time.Minute: "1m", 59*time.Minute + 59*time.Second: "59m",
		time.Hour: "1h", 47*time.Hour + 59*time.Minute: "47h", 48 * time.Hour: "2d", 100 * time.Hour: "4d",
	}
	for d, want := range cases {
		if got := Age(d); got != want {
			t.Errorf("Age(%v) = %q, want %q", d, got, want)
		}
	}
}

func rows(t *testing.T) []Row {
	t.Helper()
	other := task(2, "w-2", "engine:in-review")
	other.Claim.Host, other.Claim.Harness = "", ""
	f := (&fake{tasks: []tracker.ClaimedTask{task(1, "w-1", "engine:running"), onHost(other, "devbox"), task(30, "w-30", "engine:running")}}).
		on([]hostops.Window{win("w-1", session.StateWorking)})
	f.tasks[2].Claim.PostedAt = now.Add(-72 * time.Hour)
	return list(t, f).Rows
}

func TestWriteTable(t *testing.T) {
	var out bytes.Buffer
	if err := WriteTable(&out, Listing{Rows: rows(t)}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("got:\n%s", out.String())
	}
	header := strings.Fields(lines[0])
	if !reflect.DeepEqual(header, []string{"TASK", "HOST", "HARNESS", "STATE", "AGE", "RESETS"}) {
		t.Errorf("header %q", header)
	}
	for i, wantCells := range [][]string{
		{tracker.Ref{Repo: "o/r", Number: 1}.String(), "mac", "claude", "working", "1h", "-"},
		{tracker.Ref{Repo: "o/r", Number: 2}.String(), "devbox", "-", "in-review", "1h", "-"},
		{tracker.Ref{Repo: "o/r", Number: 30}.String(), "mac", "claude", "dead", "3d", "-"},
	} {
		if got := strings.Fields(lines[i+1]); !reflect.DeepEqual(got, wantCells) {
			t.Errorf("row %d: got %q, want %q", i, got, wantCells)
		}
	}
	// Aligned: every column starts where the header's does.
	for _, col := range []string{"HOST", "HARNESS", "STATE", "AGE", "RESETS"} {
		at := strings.Index(lines[0], col)
		for _, l := range lines[1:] {
			if at > len(l) || l[at-1] != ' ' || l[at] == ' ' {
				t.Errorf("column %s is not aligned in %q", col, l)
			}
		}
	}
}

func TestWriteTableHarnessNames(t *testing.T) {
	for _, tc := range []struct{ harness, want string }{
		{"Claude Code 2.1.310", "claude"},
		{"claude-code 2.1.310", "claude"},
		{"Codex 0.164.0", "codex"},
		{"codex-cli 0.164.0", "codex"},
		{"Other Agent 1.0", "Other"},
		{"", "-"},
	} {
		t.Run(tc.harness, func(t *testing.T) {
			claimed := task(1, "w-1", "engine:running")
			claimed.Claim.Harness = tc.harness
			f := (&fake{tasks: []tracker.ClaimedTask{claimed}}).on(nil)
			var out bytes.Buffer
			if err := WriteTable(&out, list(t, f)); err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(out.String()), "\n")
			cells := strings.Fields(lines[1])
			if len(cells) != 6 || cells[2] != tc.want {
				t.Errorf("harness column: got %q, want %q", cells, tc.want)
			}
		})
	}
}

func TestWriteTableEmpty(t *testing.T) {
	var out bytes.Buffer
	if err := WriteTable(&out, Listing{}); err != nil || out.String() != "no claimed tasks\n" {
		t.Errorf("got %q, %v", out.String(), err)
	}
}

func TestWriteJSONParsesBack(t *testing.T) {
	want := rows(t)
	var out bytes.Buffer
	if err := WriteJSON(&out, Listing{Rows: want}); err != nil {
		t.Fatal(err)
	}
	var listing Listing
	got := &listing.Rows
	if err := json.Unmarshal(out.Bytes(), &listing); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
	// Unknown liveness is null, not false, and a missing field is left out.
	var object struct {
		Sessions []map[string]any `json:"sessions"`
	}
	if err := json.Unmarshal(out.Bytes(), &object); err != nil {
		t.Fatal(err)
	}
	generic := object.Sessions
	if v, ok := generic[1]["alive"]; !ok || v != nil {
		t.Errorf("alive of a remote row: %v, %v", v, ok)
	}
	if _, ok := generic[1]["harness"]; ok {
		t.Error("an empty harness was written")
	}
	if generic[0]["alive"] != true || generic[2]["alive"] != false || generic[0]["state"] != Working {
		t.Errorf("rows: %v", generic)
	}
}

func TestWriteJSONEmptyIsAnArray(t *testing.T) {
	var out bytes.Buffer
	if err := WriteJSON(&out, Listing{}); err != nil || strings.TrimSpace(out.String()) != "{\n  \"quota\": {},\n  \"sessions\": []\n}" {
		t.Errorf("got %q, %v", out.String(), err)
	}
}
