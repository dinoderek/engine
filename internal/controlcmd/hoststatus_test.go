package controlcmd

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
)

var fleetAt = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

// twoHosts is a fleet of an unreachable host and one that runs a task, has
// quota and fails a readiness check.
func twoHosts() control.HostStatus {
	return control.HostStatus{At: fleetAt, Hosts: []control.HostRow{
		{Name: "mac", Unreachable: "ssh: connect timed out\nsecond line", Windows: []control.HostWindow{}},
		{Name: "devbox", Slots: 4, FreeSlots: 2, HeavySlots: 2, FreeHeavySlots: 1, SignedOut: []string{"codex"},
			Windows: []control.HostWindow{
				{Name: "demo-7", Alive: true, Harness: "claude", State: "working", Item: "t7", Attempt: "a9"},
				{Name: "demo-3", Alive: false, Harness: "claude"},
				{Name: "demo-8", Alive: true},
			},
			Quota: []control.HostQuota{{Harness: "claude", Window: "5h", Used: 40, ResetsAt: 1700000000}},
			Readiness: []control.HostReadiness{
				{Repo: "o/a", State: "ready"},
				{Repo: "o/b", State: "unready", Detail: "gh auth status failed (the readiness note has the output)"},
				{Repo: "o/c", State: "unknown"},
			}},
	}}
}

func TestRenderHostStatusTable(t *testing.T) {
	var out bytes.Buffer
	if err := renderHostStatus(&out, twoHosts(), fleetAt.Add(12*time.Second)); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, text := range []string{
		"mac     no: ssh: connect timed out  -                      -",
		"2 of 4 (heavy 1 of 2)",
		"claude 5h 40% to 11-14 22:13Z",
		"unready: codex signed out; o/b: gh auth status failed (the readiness note has the output); 1 repo(s) not probed yet",
		"devbox  demo-7  working  claude   t7 (a9)",
		"devbox  demo-3  exited   claude   -",
		"devbox  demo-8  live     -        -",
	} {
		if !strings.Contains(got, text) {
			t.Fatalf("missing %q in:\n%s", text, got)
		}
	}
	if strings.Contains(got, "second line") {
		t.Fatalf("a cell holds more than one line:\n%s", got)
	}
}

func TestRenderHostStatusReadyAndNoPass(t *testing.T) {
	var out bytes.Buffer
	ready := control.HostStatus{At: fleetAt, Hosts: []control.HostRow{{Name: "devbox", Slots: 1, FreeSlots: 1, Readiness: []control.HostReadiness{{Repo: "o/a", State: "ready"}}}}}
	if err := renderHostStatus(&out, ready, fleetAt); err != nil || !strings.Contains(out.String(), "yes        1 of 1 (heavy 0 of 0)  -      ready") {
		t.Fatalf("a ready host: %v\n%s", err, out.String())
	}
	out.Reset()
	if err := renderHostStatus(&out, control.HostStatus{}, fleetAt); err != nil || !strings.Contains(out.String(), "No coordinator pass has read the hosts yet.") {
		t.Fatalf("no pass: %v\n%s", err, out.String())
	}
	if got := readinessText(control.HostRow{Unready: "tmux server's directory is gone\nrestart it"}); got != "unready: tmux server's directory is gone" {
		t.Fatalf("dead tmux cwd: %q", got)
	}
}

// fleetEnv is a command environment whose server reports fleet.
func fleetEnv(t *testing.T, fleet control.HostStatus) Env {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 absent")
	}
	st, err := control.Open(filepath.Join(t.TempDir(), "db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	svc := control.Service{Store: st, Hosts: func() control.HostStatus { return fleet }}
	token := control.Secret()
	server := httptest.NewServer(control.Handler(svc, token, t.Logf))
	t.Cleanup(server.Close)
	return Env{In: strings.NewReader(""), Out: &bytes.Buffer{}, Err: &bytes.Buffer{}, Home: t.TempDir(), Getenv: func(string) string { return "" }, Client: &control.Client{URL: server.URL, Token: token}}
}

func TestHostStatusAllAgainstAServer(t *testing.T) {
	e := fleetEnv(t, twoHosts())
	if code := Main([]string{"host", "status", "--all"}, e); code != 0 {
		t.Fatalf("table: %d: %s", code, e.Err)
	}
	if out := e.Out.(*bytes.Buffer).String(); !strings.Contains(out, "devbox") || !strings.Contains(out, "t7 (a9)") || !strings.Contains(out, "ago)") {
		t.Fatalf("table:\n%s", out)
	}
	e.Out.(*bytes.Buffer).Reset()
	if code := Main([]string{"host", "status", "--all", "--json"}, e); code != 0 {
		t.Fatalf("json: %d: %s", code, e.Err)
	}
	var got control.HostStatus
	if err := json.Unmarshal(e.Out.(*bytes.Buffer).Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Hosts) != 2 || got.Hosts[1].Windows[0].Attempt != "a9" || got.Hosts[0].Unreachable == "" || !got.At.Equal(fleetAt) {
		t.Fatalf("json fleet: %+v", got)
	}
}

func TestHostStatusAllUsageAndFailures(t *testing.T) {
	e := fleetEnv(t, control.HostStatus{})
	for _, args := range [][]string{{"host"}, {"host", "status"}, {"host", "status", "--json"}, {"host", "status", "--all", "extra"}, {"host", "ready", "--all"}} {
		e.Err.(*bytes.Buffer).Reset()
		if code := Main(args, e); code != 2 || !strings.Contains(e.Err.(*bytes.Buffer).String(), "usage: engine host status --all [--json]") {
			t.Fatalf("%v: %d: %s", args, code, e.Err)
		}
	}
	if code := Main([]string{"host", "status", "--all"}, e); code != 0 || !strings.Contains(e.Out.(*bytes.Buffer).String(), "No coordinator pass") {
		t.Fatalf("no pass: %d: %s", code, e.Out)
	}
	e.Client = nil
	e.Getenv = func(k string) string {
		if k == "ENGINE_SERVER_URL" {
			return "http://127.0.0.1:1"
		}
		return ""
	}
	if code := Main([]string{"host", "status", "--all"}, e); code != 1 {
		t.Fatalf("no credential: %d", code)
	}
	e.Client = &control.Client{URL: "http://127.0.0.1:1", Token: control.Secret()}
	if code := Main([]string{"host", "status", "--all"}, e); code != 1 {
		t.Fatalf("an unreachable server: %d", code)
	}
}
