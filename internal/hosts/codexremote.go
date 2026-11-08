package hosts

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// CodexLogs is the log database in a Codex home. Its daemon logs every change
// of its remote-control connection there, the only record of whether the
// ChatGPT and Codex apps can see it.
const CodexLogs = "logs_2.sqlite"

// codexRemoteTarget is the log target of the daemon's remote-control
// connection to the apps.
const codexRemoteTarget = "codex_app_server_transport::transport::remote_control::websocket"

// codexConnected opens the record of a connection the daemon made.
const codexConnected = "connected to app-server remote control websocket"

// CodexRemoteSQL is the sqlite3 query for the newest remote-control record
// of the daemon process pid, logged in the elapsed seconds since it started.
// A record carries its process as pid:<pid>:<uuid>, and the start bound keeps
// out an earlier process that had the same pid.
func CodexRemoteSQL(pid, elapsed int) string {
	return fmt.Sprintf("select feedback_log_body as body from logs where target = '%s' and process_uuid like 'pid:%d:%%' "+
		"and ts >= cast(strftime('%%s','now') as integer) - %d order by ts desc, ts_nanos desc, id desc limit 1;",
		codexRemoteTarget, pid, elapsed+1)
}

// CodexDisconnected reads what `sqlite3 -json` printed for CodexRemoteSQL:
// empty when the daemon's newest record is a connection, else why the daemon
// is not connected. No record means it never connected since it started.
func CodexDisconnected(out string) string {
	if strings.TrimSpace(out) == "" {
		return "no connection logged since the daemon started"
	}
	var rows []struct {
		Body string `json:"body"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil || len(rows) == 0 {
		return "its log reply cannot be read"
	}
	body := strings.TrimSpace(rows[0].Body)
	if strings.HasPrefix(body, codexConnected) || strings.Contains(body, "next_status=Connected") {
		return ""
	}
	if len(body) > 120 {
		body = body[:120] + "…"
	}
	return "its last remote-control log line: " + body
}

// ParseElapsed reads the elapsed time ps prints for etime, [[dd-]hh:]mm:ss,
// as seconds. It does not depend on the host's locale or time zone, as a
// start time would.
func ParseElapsed(s string) (int, error) {
	s = strings.TrimSpace(s)
	days := 0
	if d, rest, ok := strings.Cut(s, "-"); ok {
		n, err := strconv.Atoi(d)
		if err != nil {
			return 0, fmt.Errorf("elapsed time %q: %w", s, err)
		}
		days, s = n, rest
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, fmt.Errorf("elapsed time %q is not [[dd-]hh:]mm:ss", s)
	}
	secs := 0
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("elapsed time %q is not [[dd-]hh:]mm:ss", s)
		}
		secs = secs*60 + n
	}
	return days*86400 + secs, nil
}
