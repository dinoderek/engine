package run

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/remote"
	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/start"
	"github.com/dinoderek/engine/internal/tracker"
)

func TestSystemTaskNamesItsFeature(t *testing.T) {
	gh := func(_ []string, _ string, args ...string) (string, error) {
		switch {
		case strings.HasSuffix(args[1], "/blocked_by"):
			return `[{"number":5,"state":"open","repository_url":"https://api.github.com/repos/o/r"}]`, nil
		case strings.HasSuffix(args[1], "/7"):
			return `{"number":7,"title":"T","state":"open","repository_url":"https://api.github.com/repos/o/r",` +
				`"parent_issue_url":"https://api.github.com/repos/o/r/` + `issues/3","labels":[{"name":"engine:running"}],` +
				`"body":"## Requires\nlinux\n## Harness / tier\ndeep\n"}`, nil
		}
		return "", errors.New("unexpected " + args[1])
	}
	c, op, err := System{Gh: gh, Token: "tok"}.Task(ref(7))
	if err != nil || op != ref(3) || !c.Open || !c.Has(Running) || len(c.BlockedBy) != 1 || c.BlockedBy[0].Ref != ref(5) {
		t.Errorf("card %+v, operator %v, %v", c, op, err)
	}
	// Placement reads a lone task's card as it reads a feature's.
	if !reflect.DeepEqual(c.Requires, []string{"linux"}) || !strings.Contains(c.Body, "deep") {
		t.Errorf("requires %v, body %q", c.Requires, c.Body)
	}
}

// statusClient is a host whose status is canned.
type statusClient struct {
	remote.Client
	st  hostops.Status
	err error
}

func (c statusClient) Status() (hostops.Status, error) { return c.st, c.err }

// fleetConfig is a hosts.toml of the Mac and a Linux devbox.
const fleetConfig = `default_harness = "claude"
gb_per_session = 8
reserve_gb = 8
[harness.claude]
max_sessions = 5
launch = "{bin} --model {model} --effort {effort} -n {name} --remote-control {name}"
tiers.deep = { model = "o", effort = "high" }
tiers.standard = { model = "s", effort = "high" }
tiers.light = { model = "h", effort = "low" }
[host.mac]
address = "me@mac"
harnesses = ["claude"]
ram_gb = 32
capabilities = ["macos"]
bin = { claude = "/bin/claude" }
[host.devbox]
address = "me@devbox"
harnesses = ["claude"]
ram_gb = 32
capabilities = ["linux"]
bin = { claude = "/bin/claude" }
`

// recordingClient is another host that records what it is asked to spawn.
type recordingClient struct {
	statusClient
	spawned *[]hostops.SpawnRequest
}

func (c recordingClient) SyncRepo(string) (hostops.Synced, error) { return hostops.Synced{}, nil }

func (c recordingClient) Spawn(r hostops.SpawnRequest) (hostops.Spawned, error) {
	*c.spawned = append(*c.spawned, r)
	return hostops.Spawned{Name: "r-7", Window: "@4"}, nil
}

func TestSystemStartsOnTheChosenHost(t *testing.T) {
	home := t.TempDir()
	dir := hosts.ConfigDir(home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"hosts.toml": fleetConfig, "host": "mac\n", "github-token": "tok\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gh := func(_ []string, _ string, args ...string) (string, error) {
		if strings.HasSuffix(args[1], "/blocked_by") {
			return `[]`, nil
		}
		return `{"number":7,"title":"T","state":"open","repository_url":"https://api.github.com/repos/o/r",` +
			`"parent_issue_url":"https://api.github.com/repos/o/r/` + `issues/3","labels":[],"body":"## Requires\nlinux\n"}`, nil
	}
	var spawned []hostops.SpawnRequest
	devbox := recordingClient{statusClient: statusClient{st: hostops.Status{FreeSlots: 1}}, spawned: &spawned}
	var out bytes.Buffer
	s := System{Out: &out, Starts: start.Deps{GH: gh, Home: home, Now: func() time.Time { return t0 }, Client: devbox}}
	if err := s.Start(ref(7), "devbox"); err != nil {
		t.Fatal(err)
	}
	if len(spawned) != 1 || spawned[0].Task != 7 || !strings.Contains(out.String(), "on devbox") {
		t.Errorf("spawned %+v, out %q", spawned, out.String())
	}
}

func TestSystemFleetReadsEveryHostsStatus(t *testing.T) {
	cfg, err := hosts.Parse(fleetConfig)
	if err != nil {
		t.Fatal(err)
	}
	mac := hostops.Status{FreeSlots: 1,
		Windows: []hostops.Window{{Name: "r-2", Alive: true}, {Name: "r-3"}, {Name: "run-r-1", Alive: true}},
		Runs:    []hostops.Run{{Name: "r-2", Record: session.Record{Harness: "claude"}}, {Name: "r-3", Record: session.Record{Harness: "claude"}}}}
	s := System{Config: cfg, Host: cfg.Hosts["mac"], Client: func(h hosts.Host) remote.Client {
		if h.Name == "devbox" {
			return statusClient{err: errors.New("devbox is unreachable")}
		}
		return statusClient{st: mac}
	}}
	f := s.Fleet()
	want := Fleet{Local: "mac", DefaultHarness: "claude", Caps: map[string]int{"claude": 5}, Hosts: []HostLoad{
		{Name: "devbox", Harnesses: []string{"claude"}, Capabilities: []string{"linux"}, Sessions: map[string]int{}, Unreachable: "devbox is unreachable"},
		// Only r-2 is a live session: r-3 has exited, and run-r-1 has no run.
		{Name: "mac", Harnesses: []string{"claude"}, Capabilities: []string{"macos"}, FreeSlots: 1, Sessions: map[string]int{"claude": 1},
			Live: []string{"r-2", "run-r-1"}},
	}}
	if !reflect.DeepEqual(f, want) {
		t.Errorf("fleet\n got %+v\nwant %+v", f, want)
	}
	if f.localFree() != 1 {
		t.Errorf("local free %d", f.localFree())
	}
}

func TestSystemNotesAndLog(t *testing.T) {
	home := t.TempDir()
	var out bytes.Buffer
	s := System{Home: home, Out: &out, Clock: func() time.Time { return t0 }}
	dir, err := s.NoteDir(ref(1))
	if err != nil || dir != filepath.Join(home, ".local/share/engine/runs", "r-1-coordinator") {
		t.Fatalf("note dir %q, %v", dir, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("asking for the note directory made it: %v", err)
	}
	if err := session.WriteNote(dir, LintNoted, t0); err != nil {
		t.Fatal(err)
	}
	if at, ok, err := session.ReadNote(dir, LintNoted); err != nil || !ok || !at.Equal(t0) {
		t.Errorf("the first note: %v %v %v", at, ok, err)
	}
	if _, err := s.NoteDir(tracker.Ref{Repo: "bad", Number: 1}); err == nil {
		t.Error("a bad repo: no error")
	}
	s.Logf("start %s", ref(2))
	if out.String() != "2026-10-24T13:00:00Z start "+ref(2).String()+"\n" {
		t.Errorf("log %q", out.String())
	}
}
