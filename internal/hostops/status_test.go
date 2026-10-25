package hostops

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/session"
)

// agentsFake answers `claude agents --json` with out, or fails with err.
func agentsFake(out string, err error) session.Runner {
	return func(name string, args ...string) (string, error) {
		if strings.Join(args, " ") != "agents --json" {
			return "", errors.New("unexpected " + name + " " + strings.Join(args, " "))
		}
		return out, err
	}
}

func writeRun(t *testing.T, h home, rec session.Record) string {
	t.Helper()
	dir, err := session.RunDir(h.dir, rec.RunID)
	if err == nil {
		err = session.WriteRecord(dir, rec)
	}
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestStatus(t *testing.T) {
	h := newHome(t, "mac")
	tm := &tmuxFake{
		windows: []string{"@1 %1 0 r-1", "@2 %2 0 r-2", "@3 %3 1 r-3", "@4 %4 1 r-4"},
		codes:   map[string]string{"@3": "2"},
		screens: map[string]string{"@1": "busy screen", "@2": "Allow this read?", "@3": "Error: not logged in"},
	}
	at := fixedNow()
	writeRun(t, h, session.Record{RunID: "r-1-a", Name: "r-1", Harness: Codex, Spawned: at})
	newest := writeRun(t, h, session.Record{RunID: "r-1-b", Name: "r-1", Harness: Harness, Spawned: at.Add(time.Hour)})
	other := writeRun(t, h, session.Record{RunID: "r-2-a", Name: "r-2", Harness: Harness, Spawned: at})
	d := Deps{Tmux: tm.tmux(), Home: h.dir,
		Exec: agentsFake(`[{"pid":1,"name":"r-1","status":"busy"},{"pid":2,"name":"r-2","status":"waiting","waitingFor":"a permission"}]`, nil)}
	got, err := ReadStatus(d)
	if err != nil {
		t.Fatal(err)
	}
	two := 2
	want := Status{
		Slots: 2, FreeSlots: 0,
		// Only a session that is not working shows its screen; a signal
		// leaves no exit code. The newest run picks the probe.
		Windows: []Window{
			{Name: "r-1", ID: "@1", Alive: true, Harness: Harness, State: session.StateWorking},
			{Name: "r-2", ID: "@2", Alive: true, Harness: Harness, State: session.StateWaiting, WaitingFor: "a permission", Screen: "Allow this read?"},
			{Name: "r-3", ID: "@3", ExitCode: &two, Screen: "Error: not logged in"},
			{Name: "r-4", ID: "@4"},
		},
		Agents: []session.Agent{{PID: 1, Name: "r-1", Status: "busy"}, {PID: 2, Name: "r-2", Status: "waiting", WaitingFor: "a permission"}},
		Runs: []Run{
			{Name: "r-1", Dir: newest, Record: session.Record{RunID: "r-1-b", Name: "r-1", Harness: Harness, Spawned: at.Add(time.Hour)}},
			{Name: "r-2", Dir: other, Record: session.Record{RunID: "r-2-a", Name: "r-2", Harness: Harness, Spawned: at}},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("status\n got %+v\nwant %+v", got, want)
	}
	tm.readOnly(t)
}

func TestStatusWithoutClaudeAgents(t *testing.T) {
	h := newHome(t, "mac")
	tm := &tmuxFake{windows: []string{"@1 %1 0 r-1"}, screens: map[string]string{"@1": "a screen"}}
	writeRun(t, h, session.Record{RunID: "r-1-a", Name: "r-1", Harness: Harness, Spawned: fixedNow()})
	got, err := ReadStatus(Deps{Tmux: tm.tmux(), Home: h.dir, Exec: agentsFake("", errors.New("not signed in"))})
	if err != nil {
		t.Fatal(err)
	}
	// Nothing says the session is working, so its state is unknown and its
	// screen is shown.
	if w := got.Windows[0]; !strings.Contains(got.AgentsError, "not signed in") || len(got.Agents) != 0 ||
		w.Harness != Harness || w.State != "" || w.Screen != "a screen" || got.FreeSlots != 1 {
		t.Errorf("status %+v", got)
	}
}

// Each window's newest run picks its probe. A Codex session's state comes
// from the state file its hooks write; a missing, unreadable or ended one is
// unknown, and a dead window is unknown whatever its file says.
func TestStatusStatesByHarness(t *testing.T) {
	h := newHome(t, "mac")
	at := fixedNow()
	codex := func(name string, st *session.HarnessState, body string) {
		dir := writeRun(t, h, session.Record{RunID: name + "-a", Name: name, Harness: Codex, Spawned: at})
		switch {
		case st != nil:
			if err := session.WriteHarnessState(dir, *st); err != nil {
				t.Fatal(err)
			}
		case body != "":
			write(t, dir+"/"+session.StateFile, body, 0o644)
		}
	}
	codex("x-1", &session.HarnessState{State: session.StateWorking, Event: "UserPromptSubmit", At: at}, "")
	codex("x-2", &session.HarnessState{State: session.StateWaiting, Event: "Stop", At: at}, "")
	codex("x-3", nil, "")
	codex("x-4", nil, "{not json")
	codex("x-5", &session.HarnessState{State: session.StateEnded, Event: "SessionEnd", At: at}, "")
	codex("x-6", &session.HarnessState{State: session.StateWorking, Event: "UserPromptSubmit", At: at}, "")
	writeRun(t, h, session.Record{RunID: "o-1-a", Name: "o-1", Harness: "aider", Spawned: at})
	tm := &tmuxFake{windows: []string{"@1 %1 0 x-1", "@2 %2 0 x-2", "@3 %3 0 x-3", "@4 %4 0 x-4", "@5 %5 0 x-5", "@6 %6 1 x-6",
		"@7 %7 0 o-1", "@8 %8 0 zsh", "@9 %9 0 c-1"}}
	// Claude Code lists sessions of the Codex and run-less windows' names:
	// only a window whose run is Claude's reads them.
	agents := `[{"name":"x-3","status":"busy"},{"name":"zsh","status":"busy"},{"name":"o-1","status":"busy"}]`
	got, err := ReadStatus(Deps{Tmux: tm.tmux(), Home: h.dir, Exec: agentsFake(agents, nil)})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][2]string{
		"x-1": {Codex, session.StateWorking}, "x-2": {Codex, session.StateWaiting}, "x-3": {Codex, ""}, "x-4": {Codex, ""},
		"x-5": {Codex, ""}, "x-6": {Codex, ""}, "o-1": {"aider", ""}, "zsh": {"", ""}, "c-1": {"", ""},
	}
	for _, w := range got.Windows {
		if g := [2]string{w.Harness, w.State}; g != want[w.Name] {
			t.Errorf("%s: harness and state %q, want %q", w.Name, g, want[w.Name])
		}
	}
	if len(got.Windows) != len(want) {
		t.Errorf("got %d windows", len(got.Windows))
	}
}

// Only a live window with a run under its name takes a slot: a shell, engine
// run's own window and a dead session take none.
func TestStatusSlotsFollowSessions(t *testing.T) {
	h := newHome(t, "mac") // two slots
	at := fixedNow()
	for _, name := range []string{"r-1", "r-4"} {
		writeRun(t, h, session.Record{RunID: name + "-a", Name: name, Spawned: at})
	}
	cases := []struct {
		name    string
		windows []string
		free    int
	}{
		{"a live window with a run", []string{"@1 %1 0 r-1"}, 1},
		{"a live window without one", []string{"@1 %1 0 run-o-56", "@2 %2 0 zsh"}, 2},
		{"a dead window with a run", []string{"@1 %1 1 r-4"}, 2},
		{"all three together", []string{"@1 %1 0 r-1", "@2 %2 0 zsh", "@3 %3 1 r-4"}, 1},
		{"more sessions than slots", []string{"@1 %1 0 r-1", "@2 %2 0 r-4", "@3 %3 0 r-1"}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tm := &tmuxFake{windows: c.windows}
			got, err := ReadStatus(Deps{Tmux: tm.tmux(), Home: h.dir, Exec: agentsFake("[]", nil)})
			if err != nil || got.FreeSlots != c.free || len(got.Windows) != len(c.windows) {
				t.Errorf("free slots %d, windows %d, err %v; want %d free, %d windows", got.FreeSlots, len(got.Windows), err, c.free, len(c.windows))
			}
		})
	}
}

func TestStatusOnAQuietHost(t *testing.T) {
	// A host that runs no Claude never asks for its sessions, so it has
	// none and no error to report, and no tmux session means no windows:
	// every slot is free.
	h := newHome(t, "box")
	got, err := ReadStatus(Deps{Tmux: (&tmuxFake{}).tmux(), Home: h.dir, Exec: agentsFake("", errors.New("never asked"))})
	if err != nil || got.AgentsError != "" || got.FreeSlots != 2 || len(got.Windows) != 0 || got.Runs == nil || got.Agents == nil {
		t.Errorf("status %+v, %v", got, err)
	}
}

func TestStatusFailures(t *testing.T) {
	h := newHome(t, "mac")
	if _, err := ReadStatus(Deps{Tmux: (&tmuxFake{fail: errors.New("tmux died"), windows: []string{}}).tmux(), Home: h.dir}); err == nil ||
		!strings.Contains(err.Error(), "tmux died") || isRefusal(err) {
		t.Errorf("tmux down: %v", err)
	}
	if _, err := ReadStatus(Deps{Tmux: (&tmuxFake{}).tmux(), Home: t.TempDir()}); !isRefusal(err) {
		t.Errorf("no host config: %v", err)
	}
}

// A run.json that cannot be read hides only its own run.
func TestStatusWithABadRunRecord(t *testing.T) {
	h := newHome(t, "mac")
	writeRun(t, h, session.Record{RunID: "r-1-a", Name: "r-1", Spawned: fixedNow()})
	write(t, h.dir+"/.local/share/engine/runs/r-9-x/run.json", "{", 0o644)
	got, err := ReadStatus(Deps{Tmux: (&tmuxFake{}).tmux(), Home: h.dir, Exec: agentsFake("[]", nil)})
	if err != nil || len(got.Runs) != 1 || got.Runs[0].Name != "r-1" || !strings.Contains(got.RunsError, "r-9-x/run.json") {
		t.Errorf("status %+v, %v", got, err)
	}
}
