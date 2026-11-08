package controlcmd

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/session"
)

const launchHostsTOML = `default_harness = "claude"
gb_per_session = 8
reserve_gb = 8

[harness.claude]
max_sessions = 1
launch = "{bin} --model {model} --effort {effort} -n {name}"
tiers.deep     = { model = "m", effort = "high" }
tiers.standard = { model = "m", effort = "high" }
tiers.light    = { model = "m", effort = "medium" }

[host.mac]
address = "me@mac.example.ts.net"
harnesses = ["claude"]
ram_gb = 16
bin = { claude = "~/bin/claude" }

[host.devbox]
address = "dino@devbox"
harnesses = ["claude"]
ram_gb = 16
bin = { claude = "~/bin/claude" }
`

// launchEnv is a home on host mac whose config.toml names server, with a
// private tmux server and a fake engine binary that records its arguments
// and then runs script.
type launchEnv struct {
	Env
	deps *ServerDeps
	args string
}

func newLaunchEnv(t *testing.T, server string) *launchEnv {
	t.Helper()
	home := t.TempDir()
	dir := hosts.ConfigDir(home)
	for name, content := range map[string]string{"hosts.toml": launchHostsTOML, "host": "mac\n", "config.toml": "[server]\nhost = '" + server + "'\n"} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	l := &launchEnv{args: filepath.Join(home, "args")}
	l.deps = &ServerDeps{Sleep: func(time.Duration) {}, Exec: func(io.Reader, io.Writer, io.Writer, string, ...string) (int, error) {
		t.Fatal("unexpected ssh")
		return 0, nil
	}}
	l.Env = Env{Out: &bytes.Buffer{}, Err: &bytes.Buffer{}, Home: home, Getenv: func(string) string { return "" }, Server: l.deps}
	return l
}

// withTmux gives l a tmux server of its own, killed when the test ends.
func (l *launchEnv) withTmux(t *testing.T) {
	t.Helper()
	bin, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux absent")
	}
	socket := fmt.Sprintf("engine-launch-test-%d-%d", os.Getpid(), time.Now().UnixNano())
	l.deps.Tmux = &session.Tmux{Bin: bin, Socket: socket, Run: func(name string, args ...string) (string, error) {
		return session.Exec(name, append([]string{args[0], args[1], "-f", "/dev/null"}, args[2:]...)...)
	}}
	t.Cleanup(func() {
		path, _ := l.deps.Tmux.Run(bin, "-L", socket, "display-message", "-p", "#{socket_path}")
		_ = exec.Command(bin, "-L", socket, "kill-server").Run()
		// kill-server leaves the socket file behind.
		if path = strings.TrimSpace(path); strings.Contains(path, socket) {
			_ = os.Remove(path)
		}
	})
}

// engine makes the fake engine binary record its arguments and run script.
func (l *launchEnv) engine(t *testing.T, script string) {
	t.Helper()
	path := filepath.Join(l.Home, "bin", "engine")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho \"$@\" > "+l.args+"\n"+script+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	l.deps.Executable = func() (string, error) { return path, nil }
}

// waitDead polls, against a deadline, until the server window's command has
// exited: the effect a server failing at start has.
func (l *launchEnv) waitDead(t *testing.T) func(time.Duration) {
	return func(time.Duration) {
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			windows, err := l.deps.Tmux.Windows()
			if err == nil && len(windows) == 1 && windows[0].Dead {
				return
			}
		}
		t.Error("the server window never exited")
	}
}

func (l *launchEnv) serve(args ...string) (int, string) {
	l.Out.(*bytes.Buffer).Reset()
	l.Err.(*bytes.Buffer).Reset()
	code := Main(append([]string{"serve"}, args...), l.Env)
	return code, l.Out.(*bytes.Buffer).String() + l.Err.(*bytes.Buffer).String()
}

func TestServeStartsTheServerOnceInItsOwnTmuxServer(t *testing.T) {
	l := newLaunchEnv(t, "mac")
	l.withTmux(t)
	l.engine(t, "exec sleep 60")
	code, out := l.serve()
	if code != 0 || !strings.Contains(out, "engine server started on mac at http://mac.example.ts.net:7331") {
		t.Fatalf("%d: %s", code, out)
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if b, err := os.ReadFile(l.args); err == nil && len(b) > 0 {
			if got := strings.TrimSpace(string(b)); got != "serve --foreground --listen mac.example.ts.net:7331 --advertise http://mac.example.ts.net:7331" {
				t.Fatalf("server started with %q", got)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the server window never ran engine")
		}
	}
	if code, out = l.serve(); code != 0 || !strings.Contains(out, "already running on mac") {
		t.Fatalf("second start: %d: %s", code, out)
	}
	if windows, err := l.deps.Tmux.Windows(); err != nil || len(windows) != 1 || windows[0].Name != ServerWindow {
		t.Fatalf("windows %+v, %v", windows, err)
	}
}

func TestServeReportsAServerThatExitsAtStartAndReplacesIt(t *testing.T) {
	l := newLaunchEnv(t, "mac")
	l.withTmux(t)
	l.engine(t, "echo 'listen tcp: no such host'; exit 1")
	l.deps.Sleep = l.waitDead(t)
	if code, out := l.serve(); code != 1 || !strings.Contains(out, "the server exited at start") || !strings.Contains(out, "no such host") {
		t.Fatalf("%d: %s", code, out)
	}
	l.engine(t, "exec sleep 60")
	l.deps.Sleep = func(time.Duration) {}
	if code, out := l.serve(); code != 0 || !strings.Contains(out, "started") {
		t.Fatalf("restart over the exited window: %d: %s", code, out)
	}
	if windows, err := l.deps.Tmux.Windows(); err != nil || len(windows) != 1 || windows[0].Dead {
		t.Fatalf("windows %+v, %v", windows, err)
	}
}

func TestServeHopsToTheServersHostOverSSH(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
		want int
		out  string
	}{
		{name: "started", code: 0, want: 0},
		{name: "remote failure", code: 3, want: 3},
		{name: "unreachable", code: 255, want: 1, out: "host devbox (dino@devbox) did not answer over ssh"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := newLaunchEnv(t, "devbox")
			var called []string
			l.deps.Exec = func(_ io.Reader, _, _ io.Writer, name string, args ...string) (int, error) {
				called = append([]string{name}, args...)
				return tc.code, nil
			}
			code, out := l.serve()
			if code != tc.want || !strings.Contains(out, tc.out) {
				t.Fatalf("%d: %s", code, out)
			}
			if got := strings.Join(called, " "); !strings.HasSuffix(got, "dino@devbox 'engine' 'serve' '--on-host' 'devbox'") || called[0] != "ssh" {
				t.Fatalf("ran %q", got)
			}
		})
	}
}

func TestServeRefusesWhatItCannotStart(t *testing.T) {
	for _, tc := range []struct {
		name, server string
		args         []string
		code         int
		out          string
	}{
		{name: "hop to the wrong host", server: "mac", args: []string{"--on-host", "devbox"}, code: 1, out: "this host is mac, not devbox"},
		{name: "unknown host", server: "nas", code: 1, out: `server host "nas" is not in hosts.toml`},
		{name: "server flags without foreground", server: "mac", args: []string{"--listen", "x:1"}, code: 2, out: "--listen needs --foreground"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := newLaunchEnv(t, tc.server)
			if code, out := l.serve(tc.args...); code != tc.code || !strings.Contains(out, tc.out) {
				t.Fatalf("%d: %s", code, out)
			}
		})
	}
	l := newLaunchEnv(t, "mac")
	if err := os.WriteFile(filepath.Join(hosts.ConfigDir(l.Home), "config.toml"), []byte("[server]\nurl = 'http://x:1'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out := l.serve(); code != 1 || !strings.Contains(out, "set [server] host in") {
		t.Fatalf("url only: %d: %s", code, out)
	}
}
