package codex

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupRequiresRefreshForUnrecordedOrReplacedDaemon(t *testing.T) {
	for _, receipt := range []string{"", "987 Mon Nov 2 10:00:00 2026"} {
		t.Run(receipt, func(t *testing.T) {
			c := settledCodex(boxHome)
			c.environment = receipt
			r := newCodexRig(t, c)
			if mustRun(t, r) {
				t.Fatal("unrecorded environment accepted")
			}
			if c.started != 0 || c.environment != receipt {
				t.Fatal("live daemon changed")
			}
			if !strings.Contains(r.out.String(), "finish its sessions") || !strings.Contains(r.out.String(), "app-server daemon stop") {
				t.Fatal(r.out.String())
			}
			// Operator stops the old process; setup starts and records a replacement.
			c.daemonUp, c.rcOn = false, false
			if !mustRun(t, r) || c.environment != c.instance || c.started != 1 {
				t.Fatal(r.out.String())
			}
			if !mustRun(t, r) || c.started != 1 {
				t.Fatal("settled daemon restarted")
			}
		})
	}
}

func TestSetupDoesNotAcceptAnUnrecordedStart(t *testing.T) {
	for _, missing := range []bool{false, true} {
		c := freshCodex(boxHome)
		if missing {
			c.instance = ""
		} else {
			c.environmentWriteFail = true
		}
		r := newCodexRig(t, c)
		if mustRun(t, r) {
			t.Fatal("unrecorded start accepted")
		}
		if c.environment != "" {
			t.Fatal("failed start recorded")
		}
		if !strings.Contains(r.out.String(), "11. codex daemon: OPEN") {
			t.Fatal(r.out.String())
		}
	}
}

func TestDaemonInstanceIdentifiesOnlyItsOwnProcess(t *testing.T) {
	for _, tool := range []string{"sh", "ps", "awk", "bash", "sleep"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " is not installed")
		}
	}
	home := filepath.Join(t.TempDir(), "codex")
	read := func() string {
		out, err := exec.Command("sh", "-c", daemonInstanceCommand(home)).Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	if got := read(); got != "" {
		t.Fatal(got)
	}
	program := filepath.Join(home, "packages", "release", "bin", "codex")
	startProcess(t, program, "app-server --remote-control")
	awaitRemoteOn(t, home, "on")
	got := read()
	if len(strings.Fields(got)) != 6 {
		t.Fatalf("missing process identity: %q", got)
	}
	if again := read(); again != got {
		t.Fatalf("unstable instance %q, %q", got, again)
	}
	// Codex's daemon updater runs beside the daemon from the same packages,
	// as does a daemon subcommand without remote control.
	startProcess(t, program, "app-server daemon pid-update-loop")
	awaitPs(t, program+" app-server daemon pid-update-loop")
	startProcess(t, program, "app-server --listen unix://")
	awaitPs(t, program+" app-server --listen unix://")
	if again := read(); again != got {
		t.Fatalf("the updater or a daemon without remote control changed the instance: %q, want %q", again, got)
	}
	second := filepath.Join(home, "packages", "second", "bin", "codex")
	startProcess(t, second, "app-server --remote-control")
	// Wait for the second process to appear before checking ambiguous identity.
	// The process list is a real boundary; no duration is asserted.
	awaitPs(t, second+" app-server --remote-control")
	if got := read(); got != "" {
		t.Fatalf("ambiguous identity accepted: %q", got)
	}
}

func TestDaemonReceiptUsesRealShellQuoting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon environment")
	instance := "123 Mon Nov 2 12:00:00 2026"
	cmd := daemonReceiptCommand("'"+path+"'", instance)
	if out, err := exec.Command("sh", "-c", cmd).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	out, err := exec.Command("cat", path).Output()
	if err != nil || string(out) != instance+"\n" {
		t.Fatalf("%q, %v", out, err)
	}
}
