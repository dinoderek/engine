package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/controlcmd"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/remote"
	"github.com/dinoderek/engine/internal/session"
)

// The fleet's commits and versions: every host starts on oldCommit built
// with oldGo and the plugin at oldPlugin; main's head is headCommit, whose
// go.mod names headGo, and the marketplace serves marketPlugin.
const (
	oldCommit    = "d8c1744aa2b3c4d5e6f708192a3b4c5d6e7f8091"
	headCommit   = "2a08a0c1f2a2b3c4d5e6f708192a3b4c5d6e7f80"
	oldGo        = "go1.27.2"
	headGo       = "go1.27.3"
	oldPlugin    = "0.15.0"
	marketPlugin = "0.16.0"
	serverUnit   = "engine-server.service"
)

// fleetServices is the server host's services table.
const fleetServices = "manager = \"systemd\"\nserver = \"" + serverUnit + "\"\n"

// engineBuild is an engine binary: its commit and the Go that built it.
type engineBuild struct{ commit, goVersion string }

// fleetHost is one machine of the fake fleet, with its own home.
type fleetHost struct {
	name      string
	harnesses []string
	// services is its services table's body; "" writes no table.
	services string
	home     string
	// engine is ~/.local/bin/engine, the engine its PATH finds.
	engine engineBuild
	// The operator's plugin copies, and the version each harness's
	// marketplace catalogue knows until it is refreshed.
	claude, codex, claudeMarket, codexMarket string
	// down is ssh failing to connect; setupOpen is a setup that ends open.
	down, setupOpen bool
}

func (h *fleetHost) runs(harness string) bool { return slices.Contains(h.harnesses, harness) }

// upgradeFleet is the outside world of `engine upgrade` and `engine serve`:
// the hosts, reached through Env.SSH as sh, ssh and systemctl; gh, which
// knows main's head; and the control server, stopped and started by its
// unit. The setup a run starts as the new engine's subprocess is faked here:
// it brings its host to the commit and toolchain of the engine that runs it
// and to the caller's Claude plugin, as engine setup does (its own tests
// prove that). Everything else is the real command.
type upgradeFleet struct {
	t     *testing.T
	hosts map[string]*fleetHost
	names []string
	// caller is the host the command runs on; server is the server's host.
	caller, server string
	// stack is the control server; up is whether it serves.
	stack *controlStack
	up    bool
	// log is every command that changes a host, in order, as "host: what".
	log []string
	// execs counts every program started; tmux every tmux call.
	execs, tmux int
	// backup is the backup's path on the server's host, once written.
	backup string
}

// newUpgradeFleet is the fleet of names, the caller first and the server's
// host second, every one on the old engine and plugins. Each host lists
// Claude; the caller and the server's host list Codex too.
func newUpgradeFleet(t *testing.T, names ...string) *upgradeFleet {
	t.Helper()
	f := &upgradeFleet{t: t, hosts: map[string]*fleetHost{}, names: names, caller: names[0], server: names[1], up: true}
	for i, name := range names {
		h := &fleetHost{name: name, harnesses: []string{"claude"}, home: t.TempDir(), engine: engineBuild{oldCommit, oldGo},
			claude: oldPlugin, codex: oldPlugin, claudeMarket: oldPlugin, codexMarket: oldPlugin}
		if i < 2 {
			h.harnesses = append(h.harnesses, "codex")
		}
		f.hosts[name] = h
	}
	f.hosts[f.server].services = fleetServices
	return f
}

// withServer runs the real control server on the server host's database,
// with the operator's credential where engine serve reads it.
func (f *upgradeFleet) withServer() *controlStack {
	f.t.Helper()
	home := f.hosts[f.server].home
	db := filepath.Join(home, controlcmd.ServerDBPath)
	for _, dir := range []string{filepath.Dir(db), hosts.ConfigDir(home)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			f.t.Fatal(err)
		}
	}
	f.stack = newControlStackAt(f.t, db)
	writeFile(f.t, filepath.Join(hosts.ConfigDir(home), controlcmd.ServerTokenFile), f.stack.token+"\n")
	return f.stack
}

// hostsTOML is the fleet's hosts.toml, the same file on every host.
func (f *upgradeFleet) hostsTOML() string {
	var b strings.Builder
	b.WriteString("default_harness = \"claude\"\ngb_per_session = 8\nreserve_gb = 8\n\n" +
		"[harness.claude]\nmax_sessions = 8\nlaunch = \"{bin} --model {model} --effort {effort} -n {name} --remote-control {name}\"\n" +
		"tiers.deep     = { model = \"o\", effort = \"high\" }\ntiers.standard = { model = \"s\", effort = \"high\" }\ntiers.light    = { model = \"h\", effort = \"medium\" }\n\n" +
		"[harness.codex]\nmax_sessions = 2\nlaunch = \"{bin} -m {model} -c model_reasoning_effort={effort}\"\n" +
		"tiers.deep     = { model = \"x\", effort = \"high\" }\ntiers.standard = { model = \"x\", effort = \"medium\" }\ntiers.light    = { model = \"y\", effort = \"medium\" }\n")
	for _, name := range f.names {
		h := f.hosts[name]
		bins := make([]string, len(h.harnesses))
		for i, harness := range h.harnesses {
			bins[i] = fmt.Sprintf("%[1]s = \"~/bin/%[1]s\"", harness)
		}
		fmt.Fprintf(&b, "\n[host.%s]\naddress = \"dev@%s\"\nharnesses = [\"%s\"]\nram_gb = 24\nbin = { %s }\n",
			name, name, strings.Join(h.harnesses, `", "`), strings.Join(bins, ", "))
		if h.services != "" {
			fmt.Fprintf(&b, "\n[host.%s.services]\n%s", name, h.services)
		}
	}
	return b.String()
}

// env is the Env of an engine command run on the host called on: its config
// written under its home, and every runner reaching the fake fleet.
func (f *upgradeFleet) env(on string) (Env, *bytes.Buffer, *bytes.Buffer) {
	f.t.Helper()
	h := f.hosts[on]
	dir := hosts.ConfigDir(h.home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		f.t.Fatal(err)
	}
	writeFile(f.t, filepath.Join(dir, "hosts.toml"), f.hostsTOML())
	writeFile(f.t, filepath.Join(dir, "host"), on+"\n")
	writeFile(f.t, filepath.Join(dir, "config.toml"), "[server]\nhost = \""+f.server+"\"\n")
	vars := map[string]string{}
	if f.stack != nil {
		vars["ENGINE_SERVER_URL"] = f.stack.url()
		vars["ENGINE_SERVER_TOKEN_FILE"] = filepath.Join(hosts.ConfigDir(f.hosts[f.server].home), controlcmd.ServerTokenFile)
	}
	var out, errOut bytes.Buffer
	env := Env{
		In: strings.NewReader(""), Out: &out, Err: &errOut, Home: h.home, Getenv: func(k string) string { return vars[k] },
		Now:   func() time.Time { return time.Date(2026, 11, 10, 19, 0, 0, 0, time.UTC) },
		Sleep: func(time.Duration) {}, SSH: f.execOn(on), Run: f.gh,
		Local: func(name string, args ...string) (string, error) {
			f.t.Errorf("ran %s %q on this host outside the fake fleet", name, args)
			return "", errors.New("not in this test")
		},
		ServerTmux: &session.Tmux{Bin: "tmux", Socket: controlcmd.ServerSocket, Run: func(string, ...string) (string, error) {
			f.tmux++
			return "", errors.New("no tmux in this test")
		}},
	}
	return env, &out, &errOut
}

// gh knows main's head and its go.mod, and nothing else.
func (f *upgradeFleet) gh(_ string, _ []string, _ string, args ...string) (string, error) {
	switch joined := strings.Join(args, " "); joined {
	case "api repos/dinoderek/engine/commits/main --jq .sha":
		return headCommit + "\n", nil
	case "api -H Accept: application/vnd.github.raw repos/dinoderek/engine/contents/go.mod?ref=" + headCommit:
		return "module github.com/dinoderek/engine\n\ngo 1.27.0\n\ntoolchain " + headGo + "\n", nil
	default:
		f.t.Errorf("unexpected gh %s", joined)
		return "", errors.New("unexpected gh call")
	}
}

func (f *upgradeFleet) record(host, format string, args ...any) {
	f.log = append(f.log, host+": "+fmt.Sprintf(format, args...))
}

// execOn is Env.SSH for an engine run on the host called on: its own sh and
// systemctl, ssh to the others, and the new engine it installed.
func (f *upgradeFleet) execOn(on string) func(io.Reader, io.Writer, io.Writer, string, ...string) (int, error) {
	return func(_ io.Reader, out, errOut io.Writer, name string, args ...string) (int, error) {
		f.execs++
		switch name {
		case "sh":
			return f.answer(f.hosts[on], args[len(args)-1], out, errOut), nil
		case "systemctl":
			return f.answer(f.hosts[on], name+" "+strings.Join(args, " "), out, errOut), nil
		case "ssh":
			h := f.byAddress(args[len(args)-2])
			if h.down {
				fmt.Fprintf(errOut, "ssh: connect to host %s port 22: No route to host\n", h.name)
				return 255, nil
			}
			return f.answer(h, args[len(args)-1], out, errOut), nil
		case filepath.Join(f.hosts[on].home, ".local", "bin", "engine"):
			return f.newEngine(on, out, args), nil
		}
		f.t.Errorf("ran an unexpected program %s %q", name, args)
		return -1, errors.New("unexpected program")
	}
}

func (f *upgradeFleet) byAddress(address string) *fleetHost {
	h, ok := f.hosts[strings.TrimPrefix(address, "dev@")]
	if !ok {
		f.t.Fatalf("no host at %s", address)
	}
	return h
}

// versionLine is what `engine version` prints for b.
func versionLine(b engineBuild) string {
	return fmt.Sprintf("engine v0.0.0-20261110131219-%[1]s (%[1]s, %[2]s)\n", b.commit[:12], b.goVersion)
}

// answer is h's reply to one command line: the reads first, then what
// changes it.
func (f *upgradeFleet) answer(h *fleetHost, cmd string, out, errOut io.Writer) int {
	switch cmd {
	case "engine version", `"$HOME/.local/bin/engine" version`:
		fmt.Fprint(out, versionLine(h.engine))
		return 0
	case "claude plugin list --json":
		fmt.Fprintf(out, `[{"id":"engine@engine","version":%q,"scope":"user"}]`, h.claude)
		return 0
	case "systemctl --user is-active " + serverUnit:
		if f.up {
			fmt.Fprintln(out, "active")
			return 0
		}
		fmt.Fprintln(out, "inactive")
		return 3
	}
	return f.change(h, cmd, out, errOut)
}

var (
	installLine = regexp.MustCompile(`^GOBIN="\$HOME/\.local/bin" GOTOOLCHAIN=(go[0-9.]+) go install github\.com/dinoderek/engine/cmd/engine@([0-9a-f]{40})$`)
	backupLine  = regexp.MustCompile(`^mkdir -p "\$HOME/[^"]+" && "\$HOME/\.local/bin/engine" serve --backup "\$HOME/([^"]+)"$`)
)

// change is h's reply to a command that changes it, logged.
func (f *upgradeFleet) change(h *fleetHost, cmd string, out, errOut io.Writer) int {
	codex := hosts.ShellWord(filepath.Join(h.home, "bin", "codex"))
	switch {
	case cmd == "systemctl --user stop "+serverUnit, cmd == "systemctl --user start "+serverUnit:
		f.record(h.name, "%s", cmd)
		f.serve(strings.Fields(cmd)[2] == "start")
	case installLine.MatchString(cmd):
		m := installLine.FindStringSubmatch(cmd)
		f.record(h.name, "go install %s with %s", m[2][:7], m[1])
		h.engine = engineBuild{m[2], m[1]}
	case backupLine.MatchString(cmd):
		return f.writeBackup(h, backupLine.FindStringSubmatch(cmd)[1], errOut)
	case cmd == "claude plugin marketplace update engine":
		f.record(h.name, "%s", cmd)
		h.claudeMarket = marketPlugin
	case cmd == "claude plugin update engine@engine --scope user":
		f.record(h.name, "%s", cmd)
		h.claude = h.claudeMarket
	case cmd == codex+" plugin marketplace upgrade engine":
		f.record(h.name, "codex plugin marketplace upgrade engine")
		h.codexMarket = marketPlugin
	case cmd == codex+" plugin add engine@engine":
		f.record(h.name, "codex plugin add engine@engine")
		h.codex = h.codexMarket
	case cmd == remote.ShellJoin([]string{"engine", "serve", "--on-host", h.name}):
		f.record(h.name, "engine serve --on-host %s", h.name)
		env, _, _ := f.env(h.name)
		env.Out, env.Err = out, errOut
		return Run([]string{"serve", "--on-host", h.name}, env)
	default:
		f.t.Errorf("%s: unexpected command %q", h.name, cmd)
		return 1
	}
	return 0
}

// serve starts or stops the server's unit, and the control server with it.
func (f *upgradeFleet) serve(start bool) {
	if f.stack != nil && start != f.up {
		if start {
			f.stack.start()
		} else {
			f.stack.stop()
		}
	}
	f.up = start
}

// writeBackup runs the backup as the engine at h's ~/.local/bin/engine
// would: the real `engine serve --backup` on h's database.
func (f *upgradeFleet) writeBackup(h *fleetHost, rel string, errOut io.Writer) int {
	f.record(h.name, "backup by %s", h.engine.commit[:7])
	if f.up {
		f.t.Errorf("the backup ran while the server was up")
	}
	path := filepath.Join(h.home, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		f.t.Fatal(err)
	}
	f.backup = path
	return Run([]string{"serve", "--backup", path}, Env{Out: errOut, Err: errOut, Home: h.home, Getenv: func(string) string { return "" }})
}

// newEngine is the engine the caller on installed, run as a subprocess:
// `setup` there, or `setup --host <name>` for another host. It brings the
// host to its own build, the caller's Claude plugin and, on a host that
// lists Codex, the marketplace's Codex plugin, unless that setup ends open.
func (f *upgradeFleet) newEngine(on string, out io.Writer, args []string) int {
	caller := f.hosts[on]
	name := on
	switch {
	case len(args) == 3 && args[0] == "setup" && args[1] == "--host":
		name = args[2]
	case len(args) != 1 || args[0] != "setup":
		f.t.Errorf("the new engine ran with %q", args)
		return 2
	}
	h := f.hosts[name]
	f.record(name, "setup by %s", caller.engine.commit[:7])
	fmt.Fprintf(out, "engine setup: %s\n", name)
	if h.setupOpen {
		fmt.Fprintf(out, "1 open item(s); run each command, then rerun engine setup --host %s:\n  6. gh auth: gh auth login\n", name)
		return 1
	}
	h.engine, h.claude = caller.engine, caller.claude
	if h.runs("codex") && name != on {
		h.codex = marketPlugin
	}
	fmt.Fprintf(out, "%s is set up\n", name)
	return 0
}

// seed creates a repository, a plan and a task on the server, and returns
// a seeder for more.
func (f *upgradeFleet) seed() (*seeder, control.Item) {
	f.t.Helper()
	sd := &seeder{t: f.t, s: f.stack, op: f.stack.operator(), w: &world{attempts: map[string]control.Attempt{}, windows: map[string]string{}}, workers: map[string]control.Client{}}
	plan := sd.plan(sd.repo("Alpha", "owner/alpha"), "Rollout", "## Outcome\n\nShip.\n")
	sd.call(sd.op, control.Request{Op: "approve", ID: plan.ID, Version: plan.Version})
	sd.state(sd.op, plan.ID, "planned")
	sd.task(plan, "Waiting", "## Outcome\n\nWorks.\n")
	return sd, plan
}

// tasks is the server's task list as the operator reads it, which fails
// unless the server answers.
func (f *upgradeFleet) tasks() []control.Item {
	f.t.Helper()
	res, err := f.stack.operator().Call(context.Background(), control.Request{Op: "list", State: "task"})
	if err != nil {
		f.t.Fatalf("the server does not answer: %v", err)
	}
	return res.Items
}
