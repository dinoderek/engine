package serverops

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/dinoderek/engine/internal/hosts"
)

// LogLines is how many of the unit's last log lines status shows.
const LogLines = 50

// minFreeKB is the free space below which the disk check fails: 1 GiB.
const minFreeKB = 1 << 20

// dbTimeout bounds the database check, in seconds, so a locked database
// cannot hang the status.
const dbTimeout = 30

// status runs every check, each whatever the others found, then reads the
// log. A host that cannot be reached is the one failure, and nothing else
// runs.
func status(s server, d Deps) report {
	r := newReport("status", s)
	res, err := s.run(hosts.ServerIsActive)
	if err != nil {
		r.add("reach", false, err.Error())
		return r
	}
	state := res.FirstLine()
	r.add("unit", state == "active", state)
	if err = d.Answers(); err != nil {
		r.add("api", false, err.Error())
	} else {
		r.add("api", true, "the server answers")
	}
	r.add(databaseCheck(s, d.DBPath))
	r.add(diskCheck(s, d.DBPath))
	r.Log = unitLog(s)
	return r
}

// dbOnHost is the database path as the server host's shell writes it.
func dbOnHost(rel string) string { return `"$HOME/` + rel + `"` }

// databaseCheck runs SQLite's quick check, read-only, on the database.
func databaseCheck(s server, rel string) (string, bool, string) {
	line := fmt.Sprintf("timeout %d sqlite3 -readonly %s 'PRAGMA quick_check;'", dbTimeout, dbOnHost(rel))
	res, err := s.sh.Sh(line, "")
	switch {
	case err != nil || res.Code != 0:
		return "database", false, why(res, err)
	case res.FirstLine() != "ok":
		return "database", false, "quick_check: " + strings.Join(res.Lines(), "; ")
	}
	return "database", true, "quick_check ok"
}

// diskCheck reads the free space where the database lives.
func diskCheck(s server, rel string) (string, bool, string) {
	dir := rel[:max(strings.LastIndex(rel, "/"), 0)]
	res, err := s.sh.Sh("df -Pk "+dbOnHost(dir), "")
	if err != nil || res.Code != 0 {
		return "disk", false, why(res, err)
	}
	free, size, used, ok := parseDF(res.Lines())
	if !ok {
		return "disk", false, "df said: " + strings.Join(res.Lines(), "; ")
	}
	detail := fmt.Sprintf("%s free of %s, %s used", gib(free), gib(size), used)
	if free < minFreeKB {
		return "disk", false, detail + "; under " + gib(minFreeKB) + " free"
	}
	return "disk", true, detail
}

// parseDF reads POSIX df -Pk output: a header, then one line whose second,
// fourth and fifth fields are the size and the free space in KiB and the
// percentage used.
func parseDF(lines []string) (free, size int64, used string, ok bool) {
	if len(lines) != 2 {
		return 0, 0, "", false
	}
	f := strings.Fields(lines[1])
	if len(f) < 6 {
		return 0, 0, "", false
	}
	size, errSize := strconv.ParseInt(f[1], 10, 64)
	free, errFree := strconv.ParseInt(f[3], 10, 64)
	return free, size, f[4], errSize == nil && errFree == nil
}

func gib(kb int64) string { return fmt.Sprintf("%.1f GiB", float64(kb)/(1<<20)) }

// unitLog is the unit's last log lines, or why they could not be read.
func unitLog(s server) []string {
	line := fmt.Sprintf("journalctl --user -u %s -n %d --no-pager", hosts.ShellWord(s.host.Services.Server), LogLines)
	res, err := s.sh.Sh(line, "")
	if err != nil || res.Code != 0 {
		return []string{"could not read the log: " + why(res, err)}
	}
	return res.Lines()
}
