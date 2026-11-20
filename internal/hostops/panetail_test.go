package hostops

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/session"
)

// numbered is n numbered lines, each ending in a newline.
func numbered(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	return b.String()
}

func writeLog(t *testing.T, dir, body string, written time.Time) string {
	t.Helper()
	path := filepath.Join(dir, session.PaneLog)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, written, written); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPaneTailIsTheLastLinesOfARecentLog(t *testing.T) {
	now := fixedNow()
	recent := now.Add(-time.Minute)
	long := writeLog(t, t.TempDir(), numbered(100), recent)
	if got, want := paneTailOf(long, now, time.Hour), strings.TrimSpace(strings.SplitN(numbered(100), "line 81\n", 2)[1]); got != "line 81\n"+want {
		t.Errorf("a long log gave %q", got)
	}
	short := writeLog(t, t.TempDir(), "one\r\ntwo\n\n", recent)
	if got := paneTailOf(short, now, time.Hour); got != "one\ntwo" {
		t.Errorf("a short log gave %q", got)
	}
	stale := writeLog(t, t.TempDir(), numbered(3), now.Add(-2*time.Hour))
	if got := paneTailOf(stale, now, time.Hour); got != "" {
		t.Errorf("a log written long ago gave %q", got)
	}
	if got := paneTailOf(filepath.Join(t.TempDir(), "absent"), now, time.Hour); got != "" {
		t.Errorf("a missing log gave %q", got)
	}
}

func TestPaneTailDropsTheCutLineOfALargeLog(t *testing.T) {
	now := fixedNow()
	body := strings.Repeat("x", paneTailBytes) + "\nlast line\n"
	got := paneTailOf(writeLog(t, t.TempDir(), body, now), now, time.Hour)
	if strings.Contains(got, "x") || got != "last line" {
		t.Errorf("tail of a log cut mid-line: %q", got)
	}
}

func TestStatusShowsThePaneTailOfARunWithNoLiveWindow(t *testing.T) {
	h := newHome(t, "mac")
	tm := &tmuxFake{windows: []string{"@1 %1 0 r-1", "@3 %3 1 r-3"}, screens: map[string]string{}}
	now := fixedNow()
	for _, name := range []string{"r-1", "r-2", "r-3"} {
		dir := writeRun(t, h, session.Record{RunID: name + "-a", Name: name, Harness: Claude, Spawned: now})
		writeLog(t, dir, name+" output\n", now)
	}
	d := Deps{Tmux: tm.tmux(), Home: h.dir, Now: func() time.Time { return now }, Exec: agentsFake(`[{"pid":1,"name":"r-1","status":"busy"}]`, nil)}
	got, err := ReadStatus(d)
	if err != nil {
		t.Fatal(err)
	}
	tails := map[string]string{}
	for _, r := range got.Runs {
		tails[r.Name] = r.PaneTail
	}
	if want := map[string]string{"r-1": "", "r-2": "r-2 output", "r-3": "r-3 output"}; fmt.Sprint(tails) != fmt.Sprint(want) {
		t.Errorf("tails %v, want %v", tails, want)
	}
}

func TestPaneTailStripsTerminalControlSequences(t *testing.T) {
	now := fixedNow()
	body := "\x1b[2J\x1b[1;31mError:\x1b[0m not logged in\r\n\x1b]0;title\x07\x1b[?25hnext\ttab\x00\n"
	if got := paneTailOf(writeLog(t, t.TempDir(), body, now), now, time.Hour); got != "Error: not logged in\nnext\ttab" {
		t.Errorf("tail %q", got)
	}
}
