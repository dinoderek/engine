package hostops

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/session"
)

func TestCollect(t *testing.T) {
	h := newHome(t, "mac")
	dir := writeRun(t, h, session.Record{RunID: "r-7-a", Name: "r-7", Spawned: fixedNow()})
	write(t, filepath.Join(dir, session.PaneLog), "screen\n", 0o644)
	write(t, filepath.Join(dir, "work", "notes.txt"), "x\n", 0o644)
	got, err := Collect(Deps{Home: h.dir}, "r-7-a")
	if err != nil {
		t.Fatal(err)
	}
	var rec session.Record
	if err := json.Unmarshal(got.Run, &rec); err != nil || rec.RunID != "r-7-a" || got.RunDir != dir {
		t.Errorf("collected %+v: %v", got, err)
	}
	if want := []string{"pane.log", "run.json", "work/notes.txt"}; !slices.Equal(got.Files, want) {
		t.Errorf("files %v, want %v", got.Files, want)
	}
}

func TestCollectARunWithoutARecord(t *testing.T) {
	h := newHome(t, "mac")
	write(t, filepath.Join(h.dir, session.RunsDir, "check-x", session.PermissionLog), "{}\n", 0o644)
	got, err := Collect(Deps{Home: h.dir}, "check-x")
	if err != nil || string(got.Run) != "null" || !slices.Equal(got.Files, []string{session.PermissionLog}) {
		t.Errorf("collected %+v, %v", got, err)
	}
	write(t, filepath.Join(h.dir, session.RunsDir, "bad", session.RecordFile), "{", 0o644)
	if _, err := Collect(Deps{Home: h.dir}, "bad"); err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Errorf("a bad run.json: %v", err)
	}
}

func TestCollectRefusals(t *testing.T) {
	h := newHome(t, "mac")
	for id, want := range map[string]string{"../x": `run id "../x"`, "r-7-none": "no run r-7-none on this host"} {
		if _, err := Collect(Deps{Home: h.dir}, id); !isRefusal(err) || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want a refusal containing %q", id, err, want)
		}
	}
}
