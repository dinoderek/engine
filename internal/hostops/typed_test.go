package hostops

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/session"
)

func TestASpawnLogsItsKickoffBeforeTheWindowOpens(t *testing.T) {
	h := newHome(t, "mac")
	clone := filepath.Join(h.dir, session.ControlReposDir, "o", "r")
	if err := os.MkdirAll(filepath.Join(clone, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	tm := &tmuxFake{}
	d := spawnDeps(t, h, (&gitFake{}).run, tm, nil)
	r := ControlRequest{Spawn: request(), URL: "http://127.0.0.1:7331", Credential: strings.Repeat("a", 64), Attempt: "a9", Remote: "https://github.com/o/r.git"}
	runDir, _ := session.RunDir(h.dir, r.Spawn.RunID)
	logged := ""
	tm.opening = func() {
		data, _ := os.ReadFile(filepath.Join(runDir, session.TypedLog))
		logged = string(data)
	}
	first, err := ControlSpawn(d, r)
	if err != nil {
		t.Fatal(err)
	}
	if want := session.TextHash(r.Spawn.Kickoff); !strings.Contains(logged, want) {
		t.Errorf("typed-text log %q as the window opened; want the kickoff's hash %s", logged, want)
	}
	pub, err := session.PanePublicKey(first.RunDir)
	if err != nil || first.PaneKey == "" || first.PaneKey != pub {
		t.Errorf("spawned pane key %q, the run's %q (%v)", first.PaneKey, pub, err)
	}
	replay, err := ControlSpawn(d, r)
	if err != nil || replay.PaneKey != first.PaneKey {
		t.Errorf("a replay's pane key %q, %v; want %q", replay.PaneKey, err, first.PaneKey)
	}
	collected, err := Collect(d, r.Spawn.RunID)
	if err != nil || slices.Contains(collected.Files, session.PaneKeyFile) || !slices.Contains(collected.Files, session.TypedLog) {
		t.Errorf("collected %v, %v; want the typed-text log and never the pane key", collected.Files, err)
	}
}

func TestATaskSpawnOutsideTheServerHasNoPaneKey(t *testing.T) {
	h := newHome(t, "mac")
	h.clone(t)
	tm := &tmuxFake{}
	out, err := Spawn(spawnDeps(t, h, (&gitFake{}).run, tm, nil), request())
	if err != nil {
		t.Fatal(err)
	}
	if out.PaneKey != "" {
		t.Errorf("pane key %q for a run with no server", out.PaneKey)
	}
	if _, err := os.Stat(filepath.Join(out.RunDir, session.PaneKeyFile)); err == nil {
		t.Error("wrote a pane key for a run with no server")
	}
}

func TestTheTypedOpLogsAHashInTheRun(t *testing.T) {
	h := newHome(t, "mac")
	runDir, _ := session.RunDir(h.dir, "r-7-1")
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		t.Fatal(err)
	}
	d := Deps{Home: h.dir}
	hash := session.TextHash("continue")
	var out, errs bytes.Buffer
	if code := Main([]string{"typed", "--run-id", "r-7-1", "--sha256", hash, "--json"}, d, nil, &out, &errs); code != 0 {
		t.Fatalf("exit %d: %s %s", code, out.String(), errs.String())
	}
	var reply Typed
	if err := json.Unmarshal(out.Bytes(), &reply); err != nil || reply.SHA256 != hash {
		t.Errorf("reply %q, %v", out.String(), err)
	}
	data, _ := os.ReadFile(filepath.Join(runDir, session.TypedLog))
	if !strings.Contains(string(data), hash) {
		t.Errorf("typed-text log %q lacks %s", data, hash)
	}
	for _, args := range [][]string{
		{"typed", "--run-id", "r-7-2", "--sha256", hash, "--json"}, // no such run
		{"typed", "--run-id", "r-7-1", "--sha256", "zz", "--json"},
		{"typed", "--run-id", "../x", "--sha256", hash, "--json"},
	} {
		out.Reset()
		if code := Main(args, d, nil, &out, &errs); code != 1 || !strings.Contains(out.String(), `"refused":true`) {
			t.Errorf("%v: exit %d, %s; want a refusal", args, code, out.String())
		}
	}
}
