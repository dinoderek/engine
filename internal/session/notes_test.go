package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLatestRun(t *testing.T) {
	home := t.TempDir()
	at := time.Date(2026, 10, 24, 13, 0, 0, 0, time.UTC)
	for _, r := range []Record{
		{RunID: "r-7-a", Name: "r-7", Spawned: at},
		{RunID: "r-7-b", Name: "r-7", Spawned: at.Add(time.Hour)},
		{RunID: "r-70-c", Name: "r-70", Spawned: at.Add(2 * time.Hour)},
	} {
		dir, _ := RunDir(home, r.RunID)
		if err := WriteRecord(dir, r); err != nil {
			t.Fatal(err)
		}
	}
	run, ok, err := LatestRun(home, "r-7")
	if err != nil || !ok || run.Record.RunID != "r-7-b" || filepath.Base(run.Dir) != "r-7-b" {
		t.Errorf("latest %+v %v %v", run, ok, err)
	}
	if _, ok, err := LatestRun(home, "r-8"); ok || err != nil {
		t.Errorf("r-8: %v %v", ok, err)
	}
	if _, _, err := LatestRun("", "r-7"); err == nil {
		t.Error("no home: no error")
	}
	bad, _ := RunDir(home, "r-9-x")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, RecordFile), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LatestRun(home, "r-9"); err == nil {
		t.Error("bad run.json: no error")
	}
}

func TestNotes(t *testing.T) {
	dir := t.TempDir()
	if _, ok, err := ReadNote(dir, WaitingSince); ok || err != nil {
		t.Fatalf("missing note: %v %v", ok, err)
	}
	at := time.Date(2026, 10, 24, 13, 0, 0, 0, time.FixedZone("x", 3600))
	if err := WriteNote(dir, WaitingSince, at); err != nil {
		t.Fatal(err)
	}
	got, ok, err := ReadNote(dir, WaitingSince)
	if err != nil || !ok || !got.Equal(at) {
		t.Errorf("note %v %v %v", got, ok, err)
	}
	if err := ClearNote(dir, WaitingSince); err != nil {
		t.Fatal(err)
	}
	if err := ClearNote(dir, WaitingSince); err != nil {
		t.Errorf("second clear: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, StallNoted), []byte("soon"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := ReadNote(dir, StallNoted); ok || err == nil {
		t.Errorf("bad note: %v %v", ok, err)
	}
}
