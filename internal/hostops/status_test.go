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
	writeRun(t, h, session.Record{RunID: "r-1-a", Name: "r-1", Spawned: at})
	newest := writeRun(t, h, session.Record{RunID: "r-1-b", Name: "r-1", Spawned: at.Add(time.Hour)})
	other := writeRun(t, h, session.Record{RunID: "r-2-a", Name: "r-2", Spawned: at})
	d := Deps{Tmux: tm.tmux(), Home: h.dir,
		Exec: agentsFake(`[{"pid":1,"name":"r-1","status":"busy"},{"pid":2,"name":"r-2","status":"waiting","waitingFor":"a permission"}]`, nil)}
	got, err := ReadStatus(d)
	if err != nil {
		t.Fatal(err)
	}
	two := 2
	want := Status{
		Slots: 2, FreeSlots: 0,
		// Only a session that is not busy shows its screen; a signal leaves
		// no exit code.
		Windows: []Window{
			{Name: "r-1", ID: "@1", Alive: true},
			{Name: "r-2", ID: "@2", Alive: true, Screen: "Allow this read?"},
			{Name: "r-3", ID: "@3", ExitCode: &two, Screen: "Error: not logged in"},
			{Name: "r-4", ID: "@4"},
		},
		Agents: []session.Agent{{PID: 1, Name: "r-1", Status: "busy"}, {PID: 2, Name: "r-2", Status: "waiting", WaitingFor: "a permission"}},
		Runs: []Run{
			{Name: "r-1", Dir: newest, Record: session.Record{RunID: "r-1-b", Name: "r-1", Spawned: at.Add(time.Hour)}},
			{Name: "r-2", Dir: other, Record: session.Record{RunID: "r-2-a", Name: "r-2", Spawned: at}},
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
	got, err := ReadStatus(Deps{Tmux: tm.tmux(), Home: h.dir, Exec: agentsFake("", errors.New("not signed in"))})
	if err != nil {
		t.Fatal(err)
	}
	// Nothing says the session is busy, so its screen is shown.
	if !strings.Contains(got.AgentsError, "not signed in") || len(got.Agents) != 0 || got.Windows[0].Screen != "a screen" || got.FreeSlots != 1 {
		t.Errorf("status %+v", got)
	}
}

func TestStatusOnAQuietHost(t *testing.T) {
	// A host that runs no Claude lists no agents, and no tmux session
	// means no windows: every slot is free.
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
