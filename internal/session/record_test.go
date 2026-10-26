package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecord(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("x", 3600))
	repo := Repo{Owner: "o", Name: "boga3"}
	if got := RunID(repo, 412, at); got != "boga3-412-20260102T020405Z" {
		t.Errorf("run id %s", got)
	}
	dir, err := RunDir("/home/me", "boga3-412-20260102T020405Z")
	if err != nil || dir != "/home/me/.local/share/engine/runs/boga3-412-20260102T020405Z" {
		t.Errorf("run dir %s, %v", dir, err)
	}
	for _, bad := range []string{"..", "a/b", ""} {
		if _, err := RunDir("/home/me", bad); err == nil {
			t.Errorf("run id %q accepted", bad)
		}
	}
	if _, err := RunDir("", "x"); err == nil {
		t.Error("no home accepted")
	}
	runDir := filepath.Join(t.TempDir(), "runs", "r")
	rec := Record{Schema: RecordSchema, RunID: "r", Task: "t", Heavy: true, Spawned: at.UTC()}
	if err := WriteRecord(runDir, rec); err != nil {
		t.Fatal(err)
	}
	var back Record
	if err := json.Unmarshal([]byte(readFile(filepath.Join(runDir, RecordFile))), &back); err != nil || back != rec {
		t.Errorf("read back %+v, %v", back, err)
	}
	if text := readFile(filepath.Join(runDir, RecordFile)); !strings.Contains(text, `"spawned": "2026-01-02T02:04:05Z"`) || !strings.Contains(text, `"heavy": true`) ||
		strings.Contains(text, "feature") {
		t.Errorf("run.json:\n%s", text)
	}
}

// A rewrite replaces run.json, readable by everyone, and leaves no temporary
// file beside it.
func TestWriteRecordReplacesTheFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "run")
	for _, tmux := range []string{"engine:", "engine:@9"} {
		if err := WriteRecord(dir, Record{Name: "r-7", Tmux: tmux}); err != nil {
			t.Fatal(err)
		}
	}
	rec, err := ReadRecord(dir)
	if err != nil || rec.Tmux != "engine:@9" {
		t.Errorf("record %+v, %v", rec, err)
	}
	if info, err := os.Stat(filepath.Join(dir, RecordFile)); err != nil || info.Mode().Perm() != 0o644 {
		t.Errorf("mode %v, %v", info.Mode(), err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("left %v", entries)
	}
}
