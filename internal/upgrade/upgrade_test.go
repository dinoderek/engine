package upgrade

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// wantChanges fails unless the fleet's changes are exactly want, in order.
func wantChanges(t *testing.T, f *fakeFleet, want ...string) {
	t.Helper()
	if got := f.changes(); !slices.Equal(got, want) {
		t.Fatalf("changes:\n  %s\nwant:\n  %s\noutput:\n%s", strings.Join(got, "\n  "), strings.Join(want, "\n  "), f.out.String())
	}
}

func wantOutput(t *testing.T, f *fakeFleet, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(f.out.String(), p) {
			t.Errorf("output lacks %q:\n%s", p, f.out.String())
		}
	}
}

// The run on a three-host fleet: stop and back up the server with the
// binary it ran, update the caller, set up the server's host, start the
// server, then set up the rest; it exits closed with a line per host.
func TestUpgradeRunsInOrder(t *testing.T) {
	f := newFleet(t)
	closed, err := f.run(false)
	if err != nil || !closed {
		t.Fatalf("Run = %v, %v; want closed:\n%s", closed, err, f.out.String())
	}
	wantChanges(t, f, "box: stop", "box: backup", "mac: install", "mac: plug refresh", "mac: plug update",
		"setup box", "box: start", "setup mac", "setup lab")
	backup := `mkdir -p "$HOME/.local/share/engine/server/backups" && "$HOME/.local/bin/engine" serve --backup "$HOME/.local/share/engine/server/backups/20261110T190000Z.db"`
	if f.log[1] != "box: "+backup {
		t.Errorf("backup = %q; want %q, run with the engine still on box", f.log[1], backup)
	}
	install := `GOBIN="$HOME/.local/bin" GOTOOLCHAIN=go1.27.3 go install github.com/dinoderek/engine/cmd/engine@` + mainSHA
	if f.log[2] != "mac: "+install {
		t.Errorf("install = %q; want %q", f.log[2], install)
	}
	wantOutput(t, f, "engine upgrade: 2a08a0c (go1.27.3), the head of main",
		"box   d8c1744  2a08a0c  0.9.0   set up", "lab   d8c1744  2a08a0c  0.9.0   set up", "mac   d8c1744  2a08a0c  0.9.0   set up",
		"server: engine-server.service on box was backed up to", "every host is upgraded", "engine ui is not restarted")
}

// The target is read once, and every setup runs the new ~/.local/bin/engine,
// never the running binary.
func TestUpgradeReadsTheTargetOnceAndSetsUpWithTheNewBinary(t *testing.T) {
	f := newFleet(t)
	if _, err := f.run(false); err != nil {
		t.Fatal(err)
	}
	if f.heads != 1 {
		t.Errorf("main's head read %d times; want once", f.heads)
	}
	newBin := filepath.Join(f.home, ".local", "bin", "engine")
	setups := 0
	for _, p := range f.programs {
		switch p {
		case "sh", "ssh":
		case newBin:
			setups++
		default:
			t.Errorf("ran %s; want only sh, ssh and %s", p, newBin)
		}
	}
	if setups != 3 {
		t.Errorf("%d setups ran the new engine; want 3", setups)
	}
}

// When the caller is the server's host, its commands run in sh, and its
// setup still comes first.
func TestUpgradeOnTheServersHost(t *testing.T) {
	f := newFleet(t)
	toml := strings.Replace(fleetTOML, "[host.box.services]", "[host.mac.services]", 1)
	if err := os.WriteFile(filepath.Join(f.home, ".config", "engine", "hosts.toml"), []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.home, ".config", "engine", "config.toml"), []byte("[server]\nhost = \"mac\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if closed, err := f.run(false); err != nil || !closed {
		t.Fatalf("Run = %v, %v:\n%s", closed, err, f.out.String())
	}
	wantChanges(t, f, "mac: stop", "mac: backup", "mac: install", "mac: plug refresh", "mac: plug update",
		"setup mac", "mac: start", "setup box", "setup lab")
	wantOutput(t, f, "1. here: systemctl --user stop engine-server.service")
}

// Each refusal names its cause and changes nothing: no stop, install or
// setup runs.
func TestUpgradeRefusesBeforeChangingAnything(t *testing.T) {
	tests := []struct {
		name  string
		setup func(f *fakeFleet)
		want  string
	}{
		{"a running task", func(f *fakeFleet) { f.tasks = []Task{{"t1", "done"}, {"t4", "running"}} }, "1 task(s) are running, in review or waiting on the operator: t4 (running)"},
		{"a task in review", func(f *fakeFleet) { f.tasks = []Task{{"t5", "in-review"}} }, "t5 (in-review)"},
		{"a task waiting on the operator", func(f *fakeFleet) {
			f.tasks = []Task{{"t9", "needs-human"}, {"t6", "running"}}
		}, "2 task(s) are running, in review or waiting on the operator: t6 (running), t9 (needs-human)"},
		{"no services table", func(f *fakeFleet) {
			f.writeConfig("hosts.toml", strings.Replace(fleetTOML, "[host.box.services]\nmanager = \"systemd\"\nserver = \"engine-server.service\"\n", "", 1))
		}, "host box, the server's host, records no [host.box.services] table"},
		{"an unreachable server host", func(f *fakeFleet) { f.nodes["box"].down = true }, "cannot reach box, the server's host"},
		{"an inactive unit", func(f *fakeFleet) { f.active = "inactive" }, "the server's unit engine-server.service on box is inactive, not active; start it first (ssh dev@box 'systemctl --user start engine-server.service')"},
		{"a server that does not answer", func(f *fakeFleet) { f.up = false }, "could not list the server's tasks (unit engine-server.service is active): connection refused"},
		{"no server host", func(f *fakeFleet) { f.writeConfig("config.toml", "[server]\nurl = \"http://box:7331\"\n") }, "config.toml's [server] names no host"},
		{"an unknown server host", func(f *fakeFleet) { f.writeConfig("config.toml", "[server]\nhost = \"gone\"\n") }, `config.toml's server host "gone" is not in hosts.toml`},
		{"no hosts.toml", func(f *fakeFleet) { _ = os.Remove(filepath.Join(f.home, ".config", "engine", "hosts.toml")) }, "hosts.toml"},
		{"a bad config.toml", func(f *fakeFleet) { f.writeConfig("config.toml", "[server]\n") }, "[server] must set exactly one of host and url"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFleet(t)
			tc.setup(f)
			closed, err := f.run(false)
			if err == nil || closed || !strings.Contains(err.Error(), tc.want) || !strings.HasSuffix(err.Error(), "; nothing was changed") {
				t.Fatalf("Run = %v, %v; want a refusal naming %q", closed, err, tc.want)
			}
			wantChanges(t, f)
		})
	}
}

func (f *fakeFleet) writeConfig(name, content string) {
	if err := os.WriteFile(filepath.Join(f.home, ".config", "engine", name), []byte(content), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

// A target that cannot be read refuses too.
func TestUpgradeRefusesWithoutATarget(t *testing.T) {
	f := newFleet(t)
	d := f.deps()
	d.GH = func(string, []string, string, ...string) (string, error) { return "not a sha\n", nil }
	if _, err := Run(false, d, &f.out); err == nil || !strings.Contains(err.Error(), `reads as "not a sha", not a commit; nothing was changed`) {
		t.Fatalf("Run = %v; want the unreadable head", err)
	}
	d.GH = func(_ string, _ []string, _ string, args ...string) (string, error) {
		if strings.Contains(strings.Join(args, " "), "contents/go.mod") {
			return "module x\n", nil
		}
		return mainSHA, nil
	}
	if _, err := Run(false, d, &f.out); err == nil || !strings.Contains(err.Error(), "has no toolchain line; nothing was changed") {
		t.Fatalf("Run = %v; want the missing toolchain", err)
	}
	d.GH = func(string, []string, string, ...string) (string, error) { return "", os.ErrPermission }
	if _, err := Run(false, d, &f.out); err == nil || !strings.Contains(err.Error(), "could not read the head of main") {
		t.Fatalf("Run = %v; want the failed read", err)
	}
	wantChanges(t, f)
}

// --dry-run runs only the preflight's reads, and prints every step.
func TestUpgradeDryRun(t *testing.T) {
	f := newFleet(t)
	f.nodes["lab"].down = true
	closed, err := f.run(true)
	if err != nil || !closed {
		t.Fatalf("Run = %v, %v", closed, err)
	}
	wantChanges(t, f)
	wantOutput(t, f, "engine upgrade --dry-run: nothing is changed",
		"target: 2a08a0c (go1.27.3), the head of main", "server: engine-server.service on box, active",
		"engine now: box d8c1744, lab unreachable (", "mac d8c1744",
		"1. on box: systemctl --user stop engine-server.service\n2. on box: mkdir -p",
		"3. here: GOBIN=\"$HOME/.local/bin\" GOTOOLCHAIN=go1.27.3 go install github.com/dinoderek/engine/cmd/engine@"+mainSHA,
		"4. here: plug refresh\n5. here: plug update\n6. here: ~/.local/bin/engine setup --host box\n"+
			"7. on box: systemctl --user start engine-server.service, then wait until the server answers\n"+
			"8. here: ~/.local/bin/engine setup\n9. here: ~/.local/bin/engine setup --host lab\n")
}

// An interrupt after the stop takes no further step but the start.
func TestUpgradeInterruptedStillStartsTheServer(t *testing.T) {
	f := newFleet(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.ctx, f.afterStop = ctx, cancel
	closed, err := f.run(false)
	if err != nil || closed {
		t.Fatalf("Run = %v, %v; want open", closed, err)
	}
	wantChanges(t, f, "box: stop", "box: start")
	wantOutput(t, f, "the upgrade was interrupted before step 2: engine upgrade", "box   d8c1744  -      -       not run")
}

// An interrupt before the stop changes nothing, and no start is shown.
func TestUpgradeInterruptedBeforeTheStop(t *testing.T) {
	f := newFleet(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f.ctx = ctx
	wantOpen(t, f)
	wantChanges(t, f)
	wantOutput(t, f, "OPEN: the upgrade was interrupted before step 1", "was not stopped")
	if strings.Contains(f.out.String(), "7. on box") {
		t.Errorf("the start was shown though nothing stopped:\n%s", f.out.String())
	}
}
