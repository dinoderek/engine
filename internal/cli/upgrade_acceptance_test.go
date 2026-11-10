package cli

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/control"
)

// The acceptance tests of engine upgrade and engine serve, one per
// objective, through Run against the fake fleet in upgrade_fleet_test.go.

// wantLog fails unless the fleet's changes are exactly want, in order.
func wantLog(t *testing.T, f *upgradeFleet, out string, want ...string) {
	t.Helper()
	if !slices.Equal(f.log, want) {
		t.Fatalf("changes:\n  %s\nwant:\n  %s\noutput:\n%s", strings.Join(f.log, "\n  "), strings.Join(want, "\n  "), out)
	}
}

func wantIn(t *testing.T, text string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(text, p) {
			t.Errorf("lacks %q:\n%s", p, text)
		}
	}
}

// wantRow fails unless the report has host's row with these columns.
func wantRow(t *testing.T, out string, columns ...string) {
	t.Helper()
	quoted := make([]string, len(columns))
	for i, c := range columns {
		quoted[i] = regexp.QuoteMeta(c)
	}
	if !regexp.MustCompile(`(?m)^` + strings.Join(quoted, `\s+`) + `$`).MatchString(out) {
		t.Errorf("the report lacks the row %q:\n%s", columns, out)
	}
}

// Run from the caller of a three-host fleet, every engine below main's head
// with older plugins, the upgrade leaves every host's engine at the head
// commit built with its go.mod's toolchain, every plugin at the
// marketplace's version (the operator's Claude and Codex copies on the
// caller included), every setup passed, and a line per host.
func TestUpgradeBringsEveryHostToMainsHead(t *testing.T) {
	f := newUpgradeFleet(t, "mac", "box", "lab")
	f.withServer()
	f.seed()
	env, out, errOut := f.env("mac")
	if code := Run([]string{"upgrade"}, env); code != 0 {
		t.Fatalf("exit %d; err %s\n%s", code, errOut, out)
	}
	for _, name := range f.names {
		h := f.hosts[name]
		if h.engine != (engineBuild{headCommit, headGo}) || h.claude != marketPlugin {
			t.Errorf("%s ends on %+v with Claude's plugin %s; want %s with %s, and %s", name, h.engine, h.claude, headCommit[:7], headGo, marketPlugin)
		}
		if h.runs("codex") && h.codex != marketPlugin {
			t.Errorf("%s's Codex plugin is %s; want %s", name, h.codex, marketPlugin)
		}
		wantRow(t, out.String(), name, oldCommit[:7], headCommit[:7], marketPlugin, "set up")
	}
	wantIn(t, out.String(), "engine upgrade: 2a08a0c (go1.27.3), the head of main", "every host is upgraded")
	if f.tmux != 0 {
		t.Errorf("the upgrade made %d tmux call(s)", f.tmux)
	}
}

// hosts.toml with a services table loads. Another manager, a unit without a
// manager and a manager without a unit are refused at load, by engine
// upgrade and engine serve alike, before either runs anything.
func TestServicesTableLoadsAndBadTablesAreRefused(t *testing.T) {
	f := newUpgradeFleet(t, "mac", "box", "lab")
	f.withServer()
	env, out, errOut := f.env("mac")
	if code := Run([]string{"upgrade", "--dry-run"}, env); code != 0 {
		t.Fatalf("a good table: exit %d; err %s", code, errOut)
	}
	wantIn(t, out.String(), "server: engine-server.service on box, active")
	cases := []struct{ table, want string }{
		{"manager = \"launchd\"\nserver = \"" + serverUnit + "\"\n", `host.box.services: manager "launchd" is not supported; want "systemd"`},
		{"server = \"" + serverUnit + "\"\n", `host.box.services: server needs manager = "systemd"`},
		{"manager = \"systemd\"\n", "host.box.services: manager needs server, the unit that runs the control server"},
	}
	for _, c := range cases {
		for _, command := range []string{"upgrade", "serve"} {
			f.hosts["box"].services = c.table
			env, _, errOut := f.env("mac")
			f.execs = 0
			if code := Run([]string{command}, env); code != 1 || !strings.Contains(errOut.String(), c.want) {
				t.Errorf("engine %s with %q: exit %d; want 1 and %q in:\n%s", command, c.table, code, c.want, errOut)
			}
			if f.execs != 0 || f.tmux != 0 {
				t.Errorf("engine %s with %q ran %d program(s), %d tmux call(s)", command, c.table, f.execs, f.tmux)
			}
		}
	}
	if len(f.log) != 0 {
		t.Errorf("a refused load changed %q", f.log)
	}
}

// The server's unit is stopped, the backup written by the engine the server
// ran, the hosts upgraded and the unit started again; with the server
// host's setup failing, the unit is still started.
func TestUpgradeStopsBacksUpAndStartsTheServer(t *testing.T) {
	f := newUpgradeFleet(t, "mac", "box", "lab")
	f.withServer()
	f.seed()
	env, out, errOut := f.env("mac")
	if code := Run([]string{"upgrade"}, env); code != 0 {
		t.Fatalf("exit %d; err %s\n%s", code, errOut, out)
	}
	wantLog(t, f, out.String(),
		"box: systemctl --user stop engine-server.service",
		"box: backup by d8c1744",
		"mac: go install 2a08a0c with go1.27.3",
		"mac: claude plugin marketplace update engine",
		"mac: claude plugin update engine@engine --scope user",
		"mac: codex plugin marketplace upgrade engine",
		"mac: codex plugin add engine@engine",
		"box: setup by 2a08a0c",
		"box: systemctl --user start engine-server.service",
		"mac: setup by 2a08a0c",
		"lab: setup by 2a08a0c")
	wantIn(t, out.String(), "server: engine-server.service on box was backed up to ~/.local/share/engine/server/backups/20261110T190000Z.db, and started and answers")
	if got := len(f.tasks()); got != 1 {
		t.Errorf("the restarted server lists %d task(s); want 1", got)
	}
	// The backup is a database the server opens, holding the same task.
	if !strings.HasSuffix(f.backup, "/.local/share/engine/server/backups/20261110T190000Z.db") {
		t.Fatalf("backup at %q", f.backup)
	}
	backup := newControlStackAt(t, f.backup)
	res, err := backup.operator().Call(t.Context(), control.Request{Op: "list", State: "task"})
	if err != nil || len(res.Items) != 1 || res.Items[0].Title != "Waiting" {
		t.Errorf("the backup lists %+v, %v; want the one task", res.Items, err)
	}

	f = newUpgradeFleet(t, "mac", "box", "lab")
	f.withServer()
	f.seed()
	f.hosts["box"].setupOpen = true
	env, out, _ = f.env("mac")
	if code := Run([]string{"upgrade"}, env); code != 1 {
		t.Fatalf("a failed setup on the server's host: exit %d; want 1\n%s", code, out)
	}
	at := slices.Index(f.log, "box: setup by 2a08a0c")
	if at < 0 || at+1 >= len(f.log) || f.log[at+1] != "box: systemctl --user start engine-server.service" {
		t.Fatalf("the server was not started after the failed setup: %q", f.log)
	}
	wantRow(t, out.String(), "box", oldCommit[:7], oldCommit[:7], oldPlugin, "OPEN")
	wantRow(t, out.String(), "lab", oldCommit[:7], headCommit[:7], marketPlugin, "set up")
	wantIn(t, out.String(), "and started and answers", "box: setup ended open; its report is above: ~/.local/bin/engine setup --host box")
	f.tasks()
}

// A task that is running, in review or waiting on the operator, a server
// host with no services table, and a server host that cannot be reached
// each refuse, name the cause, and change nothing.
func TestUpgradeRefusesBeforeChangingAnything(t *testing.T) {
	busy := newUpgradeFleet(t, "mac", "box", "lab")
	busy.withServer()
	sd, plan := busy.seed()
	var held []string
	for _, state := range []string{"running", "in-review", "needs-human"} {
		task := sd.task(plan, state, "## Outcome\n\nWorks.\n")
		sd.run(task, "lab", "@1")
		if state != "running" {
			sd.state(sd.workers[task.ID], task.ID, state)
		}
		held = append(held, task.ID+" ("+state+")")
	}
	noUnit := newUpgradeFleet(t, "mac", "box", "lab")
	noUnit.hosts["box"].services = ""
	down := newUpgradeFleet(t, "mac", "box", "lab")
	down.hosts["box"].down = true
	cases := []struct {
		name string
		f    *upgradeFleet
		want string
	}{
		{"busy tasks", busy, "3 task(s) are running, in review or waiting on the operator: " + strings.Join(held, ", ") + "; let them finish first"},
		{"no services table", noUnit, "host box, the server's host, records no [host.box.services] table, so upgrade cannot stop and start its server; upgrade it by hand"},
		{"unreachable server host", down, "cannot reach box, the server's host: host box is unreachable over ssh (dev@box): ssh: connect to host box port 22: No route to host"},
	}
	for _, c := range cases {
		env, out, errOut := c.f.env("mac")
		if code := Run([]string{"upgrade"}, env); code != 1 || !strings.Contains(errOut.String(), c.want+"; nothing was changed") {
			t.Errorf("%s: exit %d; want 1 and %q in:\n%s", c.name, code, c.want, errOut)
		}
		if len(c.f.log) != 0 || out.Len() != 0 || !c.f.up {
			t.Errorf("%s changed %q, printed %q, server up %v", c.name, c.f.log, out, c.f.up)
		}
	}
}

// An unreachable host and one whose setup stays open leave the rest
// upgraded; the report lists what is open per host and the exit code is 1.
func TestUpgradeFinishesTheRestPastAnUnreachableOrOpenHost(t *testing.T) {
	f := newUpgradeFleet(t, "mac", "box", "lab", "nas")
	f.withServer()
	f.seed()
	f.hosts["lab"].down = true
	f.hosts["nas"].setupOpen = true
	env, out, errOut := f.env("mac")
	if code := Run([]string{"upgrade"}, env); code != 1 {
		t.Fatalf("exit %d; want 1; err %s\n%s", code, errOut, out)
	}
	for _, name := range []string{"mac", "box"} {
		if f.hosts[name].engine.commit != headCommit {
			t.Errorf("%s was not upgraded", name)
		}
		wantRow(t, out.String(), name, oldCommit[:7], headCommit[:7], marketPlugin, "set up")
	}
	wantRow(t, out.String(), "lab", "unknown", "-", "-", "OPEN: unreachable")
	wantRow(t, out.String(), "nas", oldCommit[:7], oldCommit[:7], oldPlugin, "OPEN")
	wantIn(t, out.String(), "2 open item(s); run each command, then rerun engine upgrade:",
		"  lab could not be reached (host lab is unreachable over ssh (dev@lab): ssh: connect to host lab port 22: No route to host): ~/.local/bin/engine setup --host lab\n",
		"  nas: setup ended open; its report is above: ~/.local/bin/engine setup --host nas\n",
		"and started and answers")
	if slices.Contains(f.log, "lab: setup by 2a08a0c") {
		t.Errorf("the unreachable host's setup ran: %q", f.log)
	}
}

// engine serve on a host that records systemd starts the unit with
// systemctl --user start, on that host or over ssh from another, and opens
// no tmux window; a host with no services table still gets its tmux one.
func TestServeStartsTheUnitAndOpensNoTmuxWindow(t *testing.T) {
	for _, caller := range []string{"box", "mac"} {
		f := newUpgradeFleet(t, "mac", "box")
		f.up = false
		env, out, errOut := f.env(caller)
		if code := Run([]string{"serve"}, env); code != 0 {
			t.Fatalf("from %s: exit %d; err %s", caller, code, errOut)
		}
		want := []string{"box: systemctl --user start engine-server.service"}
		if caller == "mac" {
			want = append([]string{"box: engine serve --on-host box"}, want...)
		}
		wantLog(t, f, out.String(), want...)
		wantIn(t, out.String(), "engine server started on box at http://box:7331 (systemd unit engine-server.service)")
		if !f.up || f.tmux != 0 {
			t.Errorf("from %s: server up %v, %d tmux call(s)", caller, f.up, f.tmux)
		}
	}
	// The same host without the table goes to tmux: the fake sees it.
	f := newUpgradeFleet(t, "box", "mac")
	f.hosts["mac"].services = ""
	f.server = "box"
	env, _, _ := f.env("box")
	Run([]string{"serve"}, env)
	if f.tmux == 0 || len(f.log) != 0 {
		t.Errorf("a host with no services table: %d tmux call(s), changes %q", f.tmux, f.log)
	}
}
