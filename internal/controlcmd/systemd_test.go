package controlcmd

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/hosts"
)

// systemdEnv is launchEnv with the mac host recording a systemd unit, a tmux
// that must never be used, and a fake systemctl: active answers is-active in
// order, start answers start.
type systemdEnv struct {
	*launchEnv
	calls  []string
	active []int
	start  int
	output string
}

func newSystemdEnv(t *testing.T, server string) *systemdEnv {
	t.Helper()
	l := newLaunchEnv(t, server)
	path := filepath.Join(hosts.ConfigDir(l.Home), "hosts.toml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	toml := strings.Replace(string(b), "[host.mac]\n", "[host.mac.services]\nmanager = \"systemd\"\nserver = \"engine-server.service\"\n\n[host.mac]\n", 1)
	if err := os.WriteFile(path, []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &systemdEnv{launchEnv: l}
	l.deps.Exec = func(_ io.Reader, out, _ io.Writer, name string, args ...string) (int, error) {
		s.calls = append(s.calls, strings.Join(append([]string{name}, args...), " "))
		switch {
		case name != "systemctl":
			t.Fatalf("ran %s", name)
		case args[1] == "is-active":
			code := s.active[0]
			s.active = s.active[1:]
			return code, nil
		case args[1] == "start":
			_, _ = io.WriteString(out, s.output)
			return s.start, nil
		}
		t.Fatalf("unexpected systemctl %v", args)
		return 0, nil
	}
	return s
}

func TestServeStartsTheSystemdUnitWhenTheHostRecordsIt(t *testing.T) {
	s := newSystemdEnv(t, "mac")
	s.active = []int{3, 0}
	code, out := s.serve()
	if code != 0 || !strings.Contains(out, "engine server started on mac at http://mac.example.ts.net:7331 (systemd unit engine-server.service)") {
		t.Fatalf("%d: %s", code, out)
	}
	want := "systemctl --user is-active engine-server.service|systemctl --user start engine-server.service|systemctl --user is-active engine-server.service"
	if got := strings.Join(s.calls, "|"); got != want {
		t.Fatalf("ran %s", got)
	}
}

func TestServeLeavesAnActiveSystemdUnitAlone(t *testing.T) {
	s := newSystemdEnv(t, "mac")
	s.active = []int{0}
	code, out := s.serve()
	if code != 0 || !strings.Contains(out, "engine server already running on mac at http://mac.example.ts.net:7331 (systemd unit engine-server.service)") {
		t.Fatalf("%d: %s", code, out)
	}
	if len(s.calls) != 1 {
		t.Fatalf("ran %v", s.calls)
	}
}

func TestServeReportsASystemdStartThatFails(t *testing.T) {
	s := newSystemdEnv(t, "mac")
	s.active = []int{3}
	s.start, s.output = 1, "Failed to start engine-server.service: Unit not found."
	code, out := s.serve()
	if code != 1 || !strings.Contains(out, "Unit not found.") || !strings.Contains(out, "journalctl --user -u engine-server.service") {
		t.Fatalf("%d: %s", code, out)
	}
}

func TestServeReportsASystemdUnitThatStopsAfterItsStart(t *testing.T) {
	s := newSystemdEnv(t, "mac")
	s.active = []int{3, 3}
	code, out := s.serve()
	if code != 1 || !strings.Contains(out, "is not active after its start") || !strings.Contains(out, "journalctl --user -u engine-server.service") {
		t.Fatalf("%d: %s", code, out)
	}
}

func TestServeStillHopsToARemoteServerHostThatRecordsSystemd(t *testing.T) {
	s := newSystemdEnv(t, "devbox")
	path := filepath.Join(hosts.ConfigDir(s.Home), "hosts.toml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The server host records systemd itself: the caller still forwards.
	toml := strings.Replace(string(b), "[host.devbox]\n", "[host.devbox.services]\nmanager = \"systemd\"\nserver = \"engine-server.service\"\n\n[host.devbox]\n", 1)
	if err := os.WriteFile(path, []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	var got []string
	s.deps.Exec = func(_ io.Reader, _, _ io.Writer, name string, args ...string) (int, error) {
		got = append([]string{name}, args...)
		return 0, nil
	}
	if code, out := s.serve(); code != 0 {
		t.Fatalf("%d: %s", code, out)
	}
	if line := strings.Join(got, " "); got[0] != "ssh" || !strings.HasSuffix(line, "dino@devbox 'engine' 'serve' '--on-host' 'devbox'") {
		t.Fatalf("ran %q", line)
	}
}
