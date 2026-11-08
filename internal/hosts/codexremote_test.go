package hosts

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseElapsed(t *testing.T) {
	tests := []struct {
		in   string
		want int
		ok   bool
	}{
		{"00:07", 7, true},
		{"  12:34\n", 754, true},
		{"01:02:03", 3723, true},
		{"2-01:02:03", 2*86400 + 3723, true},
		{"", 0, false},
		{"7", 0, false},
		{"a:b", 0, false},
		{"1:2:3:4", 0, false},
		{"x-01:02", 0, false},
		{"-1:02", 0, false},
	}
	for _, tt := range tests {
		got, err := ParseElapsed(tt.in)
		if (err == nil) != tt.ok || got != tt.want {
			t.Errorf("ParseElapsed(%q) = %d, %v; want %d, ok %v", tt.in, got, err, tt.want, tt.ok)
		}
	}
}

func TestCodexDisconnected(t *testing.T) {
	long := strings.Repeat("é", 200)
	tests := []struct {
		name, out, want string
		known           bool
	}{
		{"connected", `[{"body":"connected to app-server remote control websocket server_name=mac"}]`, "", true},
		{"status connected", `[{"body":"remote control websocket status changed previous_status=Connecting next_status=Connected"}]`, "", true},
		{"no record", "\n", "no connection logged since the daemon started", true},
		{"connecting", `[{"body":"remote control websocket status changed previous_status=Connected next_status=Connecting"}]`, "its last remote-control log line: remote control websocket status changed previous_status=Connected next_status=Connecting", true},
		{"long line is cut on a rune", `[{"body":"` + long + `"}]`, "its last remote-control log line: " + strings.Repeat("é", 120) + "…", true},
		{"empty body", `[{"body":null}]`, "its last remote-control log line is empty", true},
		{"unreadable", "Error: oops", "its log reply cannot be read", false},
		{"empty list", "[]", "its log reply cannot be read", false},
	}
	for _, tt := range tests {
		if got, known := CodexDisconnected(tt.out); got != tt.want || known != tt.known {
			t.Errorf("%s: got %q, %v; want %q, %v", tt.name, got, known, tt.want, tt.known)
		}
	}
}

// The query runs against a log laid out as Codex's: it takes the newest
// websocket record of the daemon's own process since it started, and ignores
// other processes, older processes with the same PID, and other targets.
func TestCodexRemoteSQLAgainstAFixtureLog(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 absent")
	}
	db := filepath.Join(t.TempDir(), CodexLogs)
	now := time.Now().Unix()
	ws := codexRemoteTarget
	rows := []struct {
		ago, nanos            int64
		target, process, body string
	}{
		{5000, 0, ws, "pid:123:old", "connected to app-server remote control websocket (an earlier process)"},
		{50, 0, ws, "pid:123:new", "remote control websocket reader disconnected, err: websocket disconnected"},
		{40, 0, ws, "pid:123:new", "connected to app-server remote control websocket server_name=mac"},
		{30, 0, "codex_app_server_transport::transport::remote_control::client_tracker", "pid:123:new", "forwarding remote control connection closed"},
		// In one second, the later nanosecond wins, though written first.
		{20, 900, ws, "pid:999:other", "remote control websocket reader disconnected"},
		{20, 100, ws, "pid:999:other", "connected to app-server remote control websocket server_name=mac"},
	}
	script := "create table logs (id integer primary key autoincrement, ts integer not null, ts_nanos integer not null, level text not null, target text not null, feedback_log_body text, process_uuid text);\n"
	for _, r := range rows {
		script += fmt.Sprintf("insert into logs (ts, ts_nanos, level, target, feedback_log_body, process_uuid) values (%d, %d, 'INFO', '%s', '%s', '%s');\n", now-r.ago, r.nanos, r.target, r.body, r.process)
	}
	if out, err := exec.Command("sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	query := func(pid, elapsed int) string {
		t.Helper()
		out, err := exec.Command("sqlite3", "-readonly", "-json", db, CodexRemoteSQL(pid, elapsed)).Output()
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	if got, _ := CodexDisconnected(query(123, 100)); got != "" {
		t.Errorf("the daemon's newest record is its connection, got %q", got)
	}
	// Started (with the slack) after its connection: nothing since, though
	// the earlier process with its PID connected.
	if got, _ := CodexDisconnected(query(123, 10-codexStartSlack)); got != "no connection logged since the daemon started" {
		t.Errorf("got %q", got)
	}
	if got, _ := CodexDisconnected(query(999, 100)); !strings.Contains(got, "reader disconnected") {
		t.Errorf("the same-second disconnection logged later, got %q", got)
	}
}
