package upgrade

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/harness/harnesstest"
)

const (
	mainSHA  = "2a08a0c1f2a2b3c4d5e6f708192a3b4c5d6e7f80"
	oldSHA   = "d8c1744aa2b3c4d5e6f708192a3b4c5d6e7f8091"
	targetGo = "go1.27.3"
	unit     = "engine-server.service"
)

// fleetTOML is three hosts: mac, the caller; box, the server's host, with
// its unit; lab, one more.
const fleetTOML = `default_harness = "claude"
gb_per_session = 8
reserve_gb = 8

[harness.claude]
max_sessions = 8
launch = "{bin} --model {model} --effort {effort} -n {name} --remote-control {name}"
tiers.deep     = { model = "opus-x",   effort = "high" }
tiers.standard = { model = "sonnet-x", effort = "high" }
tiers.light    = { model = "haiku-x",  effort = "medium" }

[host.mac]
address = "me@mac"
harnesses = ["claude"]
ram_gb = 24
bin = { claude = "~/bin/claude" }

[host.box]
address = "dev@box"
harnesses = ["claude"]
ram_gb = 24
bin = { claude = "~/bin/claude" }

[host.box.services]
manager = "systemd"
server = "engine-server.service"

[host.lab]
address = "dev@lab"
harnesses = ["claude"]
ram_gb = 24
bin = { claude = "~/bin/claude" }
`

// node is one host of the fake fleet.
type node struct {
	name string
	rev  string // the engine on its PATH: a full commit, or "" for none
	down bool   // ssh cannot connect
	// The exit codes of what changes it: the server's stop, backup and
	// start, the install, and its setup.
	stop, backup, start, install, setup int
	// installs is what go install leaves at ~/.local/bin/engine; "" is the
	// commit asked for.
	installs string
}

// fleet is the fake world: three hosts, the server, gh, and a log of every
// command that changes something, in order.
type fakeFleet struct {
	t     *testing.T
	home  string
	nodes map[string]*node // by name
	// active is is-active's answer; up is whether the server answers.
	active string
	up     bool
	// downTries is how many task lists fail after a start before it answers.
	downTries int
	tasks     []Task
	// plugin fails, by command.
	pluginFail map[string]bool
	heads      int // reads of main's head
	log        []string
	programs   []string // every program Exec ran
	sleeps     int
	// afterStop runs once the server's stop succeeds.
	afterStop func()
	ctx       context.Context
	adapter   *harnesstest.Fake
	out       bytes.Buffer
}

func newFleet(t *testing.T) *fakeFleet {
	t.Helper()
	home := t.TempDir()
	write := func(name, content string) {
		path := filepath.Join(home, ".config", "engine", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("hosts.toml", fleetTOML)
	write("host", "mac\n")
	write("config.toml", "[server]\nhost = \"box\"\n")
	f := &fakeFleet{t: t, home: home, active: "active", up: true, pluginFail: map[string]bool{}, ctx: context.Background(),
		nodes: map[string]*node{"mac": {name: "mac", rev: oldSHA}, "box": {name: "box", rev: oldSHA}, "lab": {name: "lab", rev: oldSHA}}}
	f.adapter = &harnesstest.Fake{Called: "claude", PluginVersion: "0.9.0",
		PluginUpdate: []string{"plug refresh", "plug update"}}
	return f
}

func (f *fakeFleet) record(format string, args ...any) {
	f.log = append(f.log, fmt.Sprintf(format, args...))
}

func (f *fakeFleet) deps() Deps {
	return Deps{
		Home: f.home, Exec: f.exec, GH: f.gh, Harnesses: harness.NewAdapters(f.adapter),
		Tasks: f.listTasks, Now: func() time.Time { return time.Date(2026, 11, 10, 19, 0, 0, 0, time.UTC) },
		Sleep: func(time.Duration) { f.sleeps++ }, Context: f.ctx,
	}
}

func (f *fakeFleet) run(dryRun bool) (bool, error) {
	f.out.Reset()
	return Run(dryRun, f.deps(), &f.out)
}

func (f *fakeFleet) listTasks() ([]Task, error) {
	if !f.up {
		return nil, errors.New("connection refused")
	}
	if f.downTries > 0 {
		f.downTries--
		return nil, errors.New("connection refused")
	}
	return f.tasks, nil
}

func (f *fakeFleet) gh(_ string, _ []string, _ string, args ...string) (string, error) {
	joined := strings.Join(args, " ")
	switch {
	case strings.Contains(joined, "contents/go.mod?ref="+mainSHA):
		return "module github.com/dinoderek/engine\n\ngo 1.27.0\n\ntoolchain " + targetGo + "\n", nil
	case joined == "api repos/dinoderek/engine/commits/main --jq .sha":
		f.heads++
		return mainSHA + "\n", nil
	}
	return "", fmt.Errorf("unexpected gh %s", joined)
}

func (f *fakeFleet) byAddress(address string) *node {
	for _, n := range f.nodes {
		if "dev@"+n.name == address || "me@"+n.name == address {
			return n
		}
	}
	f.t.Fatalf("no host at %s", address)
	return nil
}

func (f *fakeFleet) exec(_ io.Reader, out, errOut io.Writer, name string, args ...string) (int, error) {
	f.programs = append(f.programs, name)
	switch name {
	case "sh":
		return f.answer(f.nodes["mac"], args[len(args)-1], out), nil
	case "ssh":
		n := f.byAddress(args[len(args)-2])
		if n.down {
			fmt.Fprintf(errOut, "ssh: connect to host %s port 22: No route to host", n.name)
			return 255, nil
		}
		return f.answer(n, args[len(args)-1], out), nil
	case filepath.Join(f.home, ".local", "bin", "engine"):
		target := f.nodes["mac"]
		if len(args) == 3 {
			target = f.nodes[args[2]]
		}
		f.record("setup %s", target.name)
		fmt.Fprintf(out, "engine setup: %s\n", target.name)
		if target.setup == 0 {
			target.rev = mainSHA
		}
		return target.setup, nil
	}
	return -1, fmt.Errorf("unexpected program %s", name)
}

func versionLine(rev string) string {
	return fmt.Sprintf("engine v0.0.0-20261110131219-%s (%s, go1.27.2)\n", rev[:12], rev[:12])
}

// answer is n's reply to one command line.
func (f *fakeFleet) answer(n *node, cmd string, out io.Writer) int {
	switch cmd {
	case "engine version":
		if n.rev == "" {
			return 127
		}
		fmt.Fprint(out, versionLine(n.rev))
		return 0
	case `"$HOME/.local/bin/engine" version`:
		installed := n.installs
		switch installed {
		case "":
			installed = mainSHA
		case "broken":
			return 126
		}
		fmt.Fprint(out, versionLine(installed))
		return 0
	case "systemctl --user is-active " + unit:
		fmt.Fprintln(out, f.active)
		if f.active != "active" {
			return 3
		}
		return 0
	}
	return f.change(n, cmd)
}

// change is n's reply to a command that changes it, logged.
func (f *fakeFleet) change(n *node, cmd string) int {
	f.record("%s: %s", n.name, cmd)
	switch {
	case cmd == "systemctl --user stop "+unit:
		if n.stop == 0 {
			f.up = false
			if f.afterStop != nil {
				f.afterStop()
			}
		}
		return n.stop
	case cmd == "systemctl --user start "+unit:
		if n.start == 0 {
			f.up = true
		}
		return n.start
	case strings.Contains(cmd, "serve --backup"):
		return n.backup
	case strings.HasPrefix(cmd, "GOBIN="):
		return n.install
	case f.pluginFail[cmd]:
		return 1
	case strings.HasPrefix(cmd, "plug "):
		return 0
	}
	f.t.Errorf("%s: unexpected command %q", n.name, cmd)
	return 1
}

// changes are the log's lines, each cut to its host and the command's first
// two words, for comparing orders.
func (f *fakeFleet) changes() []string {
	out := make([]string, len(f.log))
	for i, l := range f.log {
		host, cmd, found := strings.Cut(l, ": ")
		if !found {
			out[i] = l
			continue
		}
		words := strings.Fields(cmd)
		switch {
		case strings.HasPrefix(cmd, "systemctl"):
			cmd = words[2]
		case strings.Contains(cmd, "serve --backup"):
			cmd = "backup"
		case strings.HasPrefix(cmd, "GOBIN="):
			cmd = "install"
		}
		out[i] = host + ": " + cmd
	}
	return out
}
