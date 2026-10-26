package setup

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runRemoteOn runs the command that reads a daemon's remote control, in a real
// shell, as the target runs it.
func runRemoteOn(t *testing.T, home string) string {
	t.Helper()
	out, err := exec.Command("sh", "-c", remoteOnCommand(home)).Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// startProcess runs sleep as program, with args after it, so that `ps` shows
// "program args" as the process's command line.
func startProcess(t *testing.T, program, args string) {
	t.Helper()
	cmd := exec.Command("bash", "-c", `exec -a "$0" sleep 60`, program+" "+args)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
}

// awaitPs polls, until a deadline, for a process whose command line holds
// text to show in `ps`.
func awaitPs(t *testing.T, text string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		out, err := exec.Command("ps", "-ww", "-eo", "args=").Output()
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, text) {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no process starts with %q", text)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// awaitRemoteOn polls, until a deadline, for the process to show in `ps`.
func awaitRemoteOn(t *testing.T, home, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := runRemoteOn(t, home)
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("remote control reads %q, want %q", got, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestRemoteOnReadsOnlyTheDaemonsOwnProcess(t *testing.T) {
	for _, tool := range []string{"sh", "ps", "awk", "bash", "sleep"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " is not installed")
		}
	}
	home := filepath.Join(t.TempDir(), "codex")
	daemon := filepath.Join(home, "packages", "app-server-daemon", "releases", "0.1", "bin", "codex")
	// The shell and awk that run the command mention the path and the flag,
	// and must not count: nothing runs from the home yet.
	if got := runRemoteOn(t, home); got != "off" {
		t.Fatalf("no daemon: %q", got)
	}
	if err := os.MkdirAll(filepath.Dir(daemon), 0o755); err != nil {
		t.Fatal(err)
	}
	startProcess(t, daemon, "app-server --listen unix://")
	startProcess(t, "/elsewhere/codex", "app-server --remote-control --listen unix:// "+home+"/packages/")
	awaitPs(t, daemon+" app-server --listen")
	awaitPs(t, "/elsewhere/codex app-server --remote-control")
	if got := runRemoteOn(t, home); got != "off" {
		t.Fatalf("a daemon without remote control, and another's that only names the path: %q", got)
	}
	startProcess(t, daemon, "app-server --remote-control --listen unix://")
	awaitRemoteOn(t, home, "on")
}
