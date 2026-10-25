package start

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/session"
)

// heavyConfig makes ios and vm heavy, gives mac and dev vm, and caps mac's
// two slots at one heavy session.
func heavyConfig(t *testing.T, h home) {
	t.Helper()
	path := filepath.Join(h.dir, ".config", "engine", "hosts.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := strings.Replace(string(data), "reserve_gb = 8\n", "reserve_gb = 8\nheavy_capabilities = [\"ios\", \"vm\"]\n", 1)
	s = strings.Replace(s, `capabilities = ["macos"]`, `capabilities = ["macos", "vm"]`+"\nmax_heavy_sessions = 1", 1)
	s = strings.Replace(s, `capabilities = ["linux", "docker"]`, `capabilities = ["linux", "docker", "vm"]`, 1)
	write(t, path, s, 0o600)
}

// writeHeavyRun records a heavy run under name.
func writeHeavyRun(t *testing.T, h home, name string) {
	t.Helper()
	dir, err := session.RunDir(h.dir, name+"-a")
	if err == nil {
		err = session.WriteRecord(dir, session.Record{RunID: name + "-a", Name: name, Heavy: true, Spawned: fixedNow()})
	}
	if err != nil {
		t.Fatal(err)
	}
}

var heavyCard = issue{number: 7, state: "open", parent: 3, body: "## Requires\nvm\n\n" + card("deep")}

func TestAHeavyTaskIsRefusedOnFullHeavySlots(t *testing.T) {
	checkRefusal(t, refusal{name: "heavy slots full", issue: heavyCard, windows: []string{"@1 %1 0 r-1"},
		setup: func(t *testing.T, h home) { heavyConfig(t, h); writeHeavyRun(t, h, "r-1") },
		want:  "host mac has no free heavy slot for " + ref(7).String() + ": 1 heavy sessions running, 1 heavy slots"})
}

// With mac's one heavy slot taken, a light task still starts, and a heavy
// one does once the heavy session's window has died. Windows with no run hold
// nothing.
func TestHeavySlotsLeaveLightTasksAlone(t *testing.T) {
	cases := []struct {
		name    string
		issue   issue
		windows []string
		heavy   bool
	}{
		{"a light task", startable, []string{"@1 %1 0 r-1", "@2 %2 0 run-o-3", "@3 %3 0 zsh"}, false},
		{"a heavy task beside a dead heavy session", heavyCard, []string{"@1 %1 1 r-1", "@2 %2 0 run-o-3"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHome(t, "mac")
			heavyConfig(t, h)
			writeHeavyRun(t, h, "r-1")
			gh := &ghFake{issue: c.issue}
			tm := &tmuxFake{windows: c.windows}
			d := Deps{GH: gh.run, Tmux: &session.Tmux{Run: tm.run, Bin: "tmux"}, Home: h.dir, Getenv: func(string) string { return "" }}
			p, err := check(Options{Ref: ref(7)}, d)
			if err != nil {
				t.Fatal(err)
			}
			if p.heavy != c.heavy || p.spawnRequest("r-7-x").Heavy != c.heavy {
				t.Errorf("heavy %v, spawn request %+v; want heavy %v", p.heavy, p.spawnRequest("r-7-x"), c.heavy)
			}
		})
	}
}

// On another machine, the heavy check reads its status's free heavy slots.
func TestAHeavyTaskOnAnotherHost(t *testing.T) {
	cases := []struct {
		name   string
		issue  issue
		status hostops.Status
		want   string
	}{
		{"heavy, none free", heavyCard, hostops.Status{Slots: 3, FreeSlots: 2, HeavySlots: 1}, "host dev has no free heavy slot for " + ref(7).String() + ": 1 heavy slots"},
		{"heavy, one free", heavyCard, hostops.Status{Slots: 3, FreeSlots: 2, HeavySlots: 1, FreeHeavySlots: 1}, ""},
		{"light, none free", startable, hostops.Status{Slots: 3, FreeSlots: 2, HeavySlots: 1}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHome(t, "mac")
			heavyConfig(t, h)
			f := &hostFake{status: tc.status}
			err := Run(Options{Ref: ref(7), Host: "dev"}, remoteDeps(t, h, &ghFake{issue: tc.issue}, f), &bytes.Buffer{})
			if tc.want == "" {
				if err != nil || len(f.spawned) != 1 || f.spawned[0].Heavy != (tc.issue.body == heavyCard.body) {
					t.Errorf("err = %v, spawned %+v", err, f.spawned)
				}
				return
			}
			var refused *RefusedError
			if !errors.As(err, &refused) || !strings.Contains(err.Error(), tc.want) || len(f.spawned) != 0 {
				t.Errorf("err = %v, want a refusal containing %q; spawned %+v", err, tc.want, f.spawned)
			}
		})
	}
}
