package upgrade

import (
	"errors"
	"strings"
	"testing"
)

// wantOpen runs the upgrade and fails unless it ends open, without a refusal.
func wantOpen(t *testing.T, f *fakeFleet) {
	t.Helper()
	closed, err := f.run(false)
	if err != nil || closed {
		t.Fatalf("Run = %v, %v; want open:\n%s", closed, err, f.out.String())
	}
}

// A failing setup on the server's host, a failing caller install and a
// failing backup each still start the server, and the report says so.
func TestUpgradeStartsTheServerAfterAFailure(t *testing.T) {
	t.Run("the server host's setup", func(t *testing.T) {
		f := newFleet(t)
		f.nodes["box"].setup = 1
		wantOpen(t, f)
		wantChanges(t, f, "box: stop", "box: backup", "mac: install", "mac: plug refresh", "mac: plug update",
			"setup box", "box: start", "setup mac", "setup lab")
		wantOutput(t, f, "box   d8c1744  d8c1744  0.9.0   OPEN",
			"box: setup ended open; its report is above: ~/.local/bin/engine setup --host box", "and started and answers")
	})
	t.Run("the caller's install", func(t *testing.T) {
		f := newFleet(t)
		f.nodes["mac"].install = 1
		wantOpen(t, f)
		wantChanges(t, f, "box: stop", "box: backup", "mac: install", "box: start")
		wantOutput(t, f, "OPEN: go install failed: exit status 1", "lab   d8c1744  -      -       not run",
			"lab was not set up: the upgrade stopped first: engine upgrade", "and started and answers")
	})
	t.Run("the backup", func(t *testing.T) {
		f := newFleet(t)
		f.nodes["box"].backup = 1
		wantOpen(t, f)
		wantChanges(t, f, "box: stop", "box: backup", "box: start")
		wantOutput(t, f, "the backup failed: exit status 1: ssh dev@box 'mkdir -p", "was not backed up; it started and answers")
	})
	t.Run("the stop", func(t *testing.T) {
		f := newFleet(t)
		f.nodes["box"].stop = 1
		wantOpen(t, f)
		wantChanges(t, f, "box: stop", "box: start")
		wantOutput(t, f, "the server's stop failed: exit status 1: ssh dev@box 'systemctl --user stop engine-server.service'")
	})
	t.Run("an installed engine that does not run", func(t *testing.T) {
		f := newFleet(t)
		f.nodes["mac"].installs = "broken"
		wantOpen(t, f)
		wantChanges(t, f, "box: stop", "box: backup", "mac: install", "box: start")
		wantOutput(t, f, "the installed engine does not run: exit status 126")
	})
	t.Run("a server host lost after the stop", func(t *testing.T) {
		f := newFleet(t)
		f.afterStop = func() { f.nodes["box"].down = true }
		wantOpen(t, f)
		wantChanges(t, f, "box: stop")
		wantOutput(t, f, "the backup failed: host box is unreachable over ssh", "the server did not start: host box is unreachable over ssh")
	})
	t.Run("an engine that is not the target after the install", func(t *testing.T) {
		f := newFleet(t)
		f.nodes["mac"].installs = oldSHA
		wantOpen(t, f)
		wantChanges(t, f, "box: stop", "box: backup", "mac: install", "box: start")
		wantOutput(t, f, "~/.local/bin/engine is d8c1744 after the install, not 2a08a0c: engine upgrade")
	})
}

// A start that fails, or a server that never answers, names the journalctl
// command that reads its log; one that answers late is waited for.
func TestUpgradeServerStart(t *testing.T) {
	journal := "ssh dev@box 'journalctl --user -u engine-server.service'"
	t.Run("a failed start", func(t *testing.T) {
		f := newFleet(t)
		f.nodes["box"].start = 1
		wantOpen(t, f)
		wantOutput(t, f, "the server did not start: exit status 1; read its log: "+journal, "is open: see below")
	})
	t.Run("a server that does not answer", func(t *testing.T) {
		f := newFleet(t)
		f.afterStop = func() { f.downTries = 1000 }
		wantOpen(t, f)
		wantOutput(t, f, "the server started but did not answer within 30s; read its log: "+journal)
		if f.sleeps != answerTries-1 {
			t.Errorf("slept %d times; want %d", f.sleeps, answerTries-1)
		}
	})
	t.Run("a server that answers late", func(t *testing.T) {
		f := newFleet(t)
		f.afterStop = func() { f.downTries = 3 }
		if closed, err := f.run(false); err != nil || !closed {
			t.Fatalf("Run = %v, %v:\n%s", closed, err, f.out.String())
		}
		if f.sleeps != 3 {
			t.Errorf("slept %d times; want 3", f.sleeps)
		}
	})
}

// An unreachable host and one whose setup stays open leave the others set
// up, are open in the report, and the run exits open.
func TestUpgradeGoesOnPastAHostThatStaysOpen(t *testing.T) {
	f := newFleet(t)
	f.nodes["lab"].down = true
	f.nodes["mac"].setup = 1
	wantOpen(t, f)
	wantChanges(t, f, "box: stop", "box: backup", "mac: install", "mac: plug refresh", "mac: plug update",
		"setup box", "box: start", "setup mac")
	wantOutput(t, f, "9. here: ~/.local/bin/engine setup --host lab\n   skipped: lab could not be reached at the preflight",
		"box   d8c1744  2a08a0c  0.9.0   set up", "lab   unknown  -        -       OPEN: unreachable",
		"mac   d8c1744  d8c1744  0.9.0   OPEN", "2 open item(s)",
		"mac: setup ended open; its report is above: ~/.local/bin/engine setup\n",
		"lab could not be reached (host lab is unreachable over ssh (dev@lab)")
}

// A plugin update that fails, or that its adapter cannot give, stays open
// and the run goes on.
func TestUpgradePluginUpdateStaysOpen(t *testing.T) {
	t.Run("a failed command", func(t *testing.T) {
		f := newFleet(t)
		f.pluginFail["plug refresh"] = true
		wantOpen(t, f)
		wantChanges(t, f, "box: stop", "box: backup", "mac: install", "mac: plug refresh", "mac: plug update",
			"setup box", "box: start", "setup mac", "setup lab")
		wantOutput(t, f, "plug refresh failed: exit status 1: plug refresh")
	})
	t.Run("no commands", func(t *testing.T) {
		f := newFleet(t)
		f.adapter.PluginUpdate, f.adapter.PluginUpdateErr = nil, errors.New("host mac has no bin")
		wantOpen(t, f)
		wantOutput(t, f, "4. here: claude's plugin update: host mac has no bin\n   OPEN: claude's plugin update: host mac has no bin")
		if !strings.Contains(strings.Join(f.changes(), ","), "setup lab") {
			t.Errorf("the run stopped: %v", f.changes())
		}
	})
}
