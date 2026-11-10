package codex

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/harness/harnesstest"
	"github.com/dinoderek/engine/internal/hosts"
)

const (
	callerPlugin    = "0.7.1"
	pluginIDForTest = "engine@engine"
	secret          = "ghp_secret_token"
)

// box is a host's shell as the Codex steps reach it: it records every
// command, and the ones that changed something.
type box struct {
	codex     *codexState
	mutations []string
	commands  []string
}

func (b *box) mutate(cmd string) { b.mutations = append(b.mutations, cmd) }

// sh answers one command line, as setup's target would.
func (b *box) sh(cmd, stdin string) (harness.Result, error) {
	b.commands = append(b.commands, cmd)
	var out, errOut strings.Builder
	code, ok := b.codex.answer(b, cmd, stdin, &out, &errOut)
	if !ok {
		return harness.Result{ErrOut: fmt.Sprintf("box: unexpected command %q", cmd), Code: 99}, nil
	}
	return harness.Result{Out: out.String(), ErrOut: errOut.String(), Code: code}, nil
}

// rig runs the Codex steps on a box, numbered as a setup of another host
// that also runs Claude numbers them: the core's four steps and Claude's two
// come first.
type rig struct {
	box *box
	tgt *harnesstest.Target
	out strings.Builder
}

// newCodexRig is a rig on the devbox, whose Codex the case decides.
func newCodexRig(t *testing.T, codex *codexState) *rig {
	t.Helper()
	b := &box{codex: codex}
	return &rig{box: b, tgt: &harnesstest.Target{
		Shell: b.sh, Address: "dev@box", Version: callerPlugin,
		Entry:  hosts.Host{Name: "box", Harnesses: []string{"claude", Name}, Bin: map[string]string{Name: "~/.local/bin/codex"}},
		Before: []string{"tools", "engine", "config", "no suspend", "plugin", "sign-in"},
	}}
}

// local makes the rig this host checking itself, whose setup has no config
// step and no caller's plugin version.
func (r *rig) local() {
	r.tgt.Address, r.tgt.Version = "", ""
	r.tgt.Before = []string{"tools", "engine", "no suspend", "plugin", "sign-in"}
}

// mustRun runs the steps and reports whether every one closed.
func mustRun(t *testing.T, r *rig) bool {
	t.Helper()
	report, err := harnesstest.RunSteps(New(harness.Env{}, Options{}), r.tgt)
	if err != nil {
		t.Fatalf("setup failed: %v\n%s", err, report)
	}
	r.out.Reset()
	r.out.WriteString(report)
	return !strings.Contains(report, ": OPEN")
}

// codexState is a host's Codex: the pinned binary, the operator's login, and
// what is in engine's Codex home. Its commands are the ones setup runs.
type codexState struct {
	home, userHome string
	bin            string         // where the binary runs; "" is nowhere
	onPath         string         // what `command -v codex` finds when bin is absent
	operatorDaemon bool           // the operator's own daemon runs
	userPlugin     string         // version of engine's plugin in the operator's home; "" is none
	signedIn       bool           // the operator's Codex
	userAuth       bool           // the operator's auth.json is a file
	operatorRC     harness.Result // `codex remote-control --json` in the operator's home

	config     *string // engine's config.toml; nil is none
	authLink   string  // "", "FILE", or the link's target
	plugin     string  // version of engine's plugin in engine's home; "" is none
	marketFail bool
	// pluginLeaves is the version an install leaves, when not the caller's;
	// leavesNothing makes it leave none. They apply to both homes.
	pluginLeaves  string
	leavesNothing bool
	// userMarketFail makes the operator's home's plugin commands fail.
	userMarketFail bool
	writeFail      bool
	// loginBroken makes engine's home reject the login it links.
	loginBroken          bool
	daemonUp             bool
	rcOn                 bool // the daemon runs with remote control on
	startFail            bool
	startNoRC            bool // a start leaves remote control off
	startExit1           bool // a start exits 1 though the daemon runs, as on an errored connection
	started              int
	instance             string
	environment          string
	environmentWriteFail bool
	// etime is ps's elapsed time for the daemon, and log what sqlite3 prints
	// for its newest remote-control record; logFail fails the read.
	etime   string
	log     string
	logFail bool
}

func (c *codexState) engineHome() string { return c.home + "/.local/share/engine/codex" }

// settledCodex is a host with Codex fully set up.
func settledCodex(home string) *codexState {
	c := &codexState{home: home, userHome: home + "/.codex", bin: home + "/.local/bin/codex", signedIn: true, userAuth: true,
		operatorRC: harness.Result{Out: `{"status":"connected"}`}, plugin: callerPlugin, userPlugin: callerPlugin, operatorDaemon: true, daemonUp: true, rcOn: true}
	c.instance = "123 Mon Nov 2 12:00:00 2026"
	c.etime, c.log = "01:02:03", `[{"body":"connected to app-server remote control websocket server_name=box"}]`
	c.environment = c.instance
	cfg := withEngineBlock("[marketplaces.engine]\nsource_type = \"git\"\n", engineBlock(home))
	c.config, c.authLink = &cfg, c.userHome+"/auth.json"
	return c
}

// freshCodex is a host with Codex installed and signed in, and nothing of
// engine's yet.
func freshCodex(home string) *codexState {
	c := settledCodex(home)
	c.config, c.authLink, c.plugin, c.daemonUp, c.rcOn = nil, "", "", false, false
	c.environment = ""
	return c
}

func (c *codexState) engineSignedIn() bool { return c.signedIn && c.authLink != "" && !c.loginBroken }

// answer replies to a Codex command, and reports whether it was one.
func (c *codexState) answer(b *box, cmd, stdin string, out, errOut io.Writer) (int, bool) {
	say := func(format string, args ...any) (int, bool) { fmt.Fprintf(out, format, args...); return 0, true }
	fail := func(code int, msg string) (int, bool) { fmt.Fprint(errOut, msg); return code, true }
	eh := c.engineHome()
	switch {
	case strings.HasPrefix(cmd, "printf '%s\\n%s\\n'"):
		return say("%s\n%s\n", c.home, c.userHome)
	case cmd == "command -v codex":
		if c.onPath == "" {
			return 1, true
		}
		return say("%s\n", c.onPath)
	case cmd == "cat "+eh+"/config.toml":
		if c.config == nil {
			return fail(1, "cat: no such file")
		}
		return say("%s", *c.config)
	case strings.HasPrefix(cmd, "umask 077 && mkdir -p "+eh+" && cat > "):
		b.mutate(cmd)
		if c.writeFail {
			return fail(1, "cat: No space left on device")
		}
		c.config = &stdin
		return 0, true
	case strings.Contains(cmd, "readlink"):
		return c.linkState(out)
	case cmd == "[ -f "+c.userHome+"/auth.json ]":
		return boolCode(c.userAuth), true
	case strings.HasPrefix(cmd, "umask 077 && mkdir -p "+eh+" && ln -sfn "):
		b.mutate(cmd)
		c.authLink = c.userHome + "/auth.json"
		return 0, true
	case cmd == daemonInstanceCommand(eh):
		if c.daemonUp {
			return say("%s\n", c.instance)
		}
		return say("")
	case cmd == "cat "+eh+"/daemon-environment":
		if c.environment == "" {
			return fail(1, "missing receipt")
		}
		return say("%s\n", c.environment)
	case strings.HasPrefix(cmd, "umask 077 && printf "):
		b.mutate(cmd)
		if c.environmentWriteFail {
			return fail(1, "read-only file system")
		}
		c.environment = c.instance
		return 0, true
	case cmd == "ps -o etime= -p 123":
		return say("%s\n", c.etime)
	case cmd == remoteConnectionCommand(eh, 123, 3723) || cmd == remoteConnectionCommand(eh, 123, 30):
		if c.logFail {
			return fail(1, "Error: unable to open database")
		}
		return say("%s", c.log)
	case cmd == remoteOnCommand(eh):
		if c.rcOn && c.daemonUp {
			return say("on\n")
		}
		return say("off\n")
	case strings.HasPrefix(cmd, "unset "+daemonDrops+" && "):
		return c.start(b, cmd, errOut)
	}
	if args, found := strings.CutPrefix(cmd, "CODEX_HOME="+eh+" "+c.bin+" "); found {
		return c.inEngine(b, args, out, errOut)
	}
	if args, found := strings.CutPrefix(cmd, c.bin+" "); found && c.bin != "" {
		return c.operator(b, args, out, errOut)
	}
	return 0, false
}

func boolCode(ok bool) int {
	if ok {
		return 0
	}
	return 1
}

func (c *codexState) linkState(out io.Writer) (int, bool) {
	switch c.authLink {
	case "":
		fmt.Fprintln(out, "NONE")
	default:
		fmt.Fprintln(out, c.authLink)
	}
	return 0, true
}

// operator answers the commands in the operator's Codex home.
func (c *codexState) operator(b *box, args string, out, errOut io.Writer) (int, bool) {
	switch args {
	case "--version":
		fmt.Fprintln(out, "codex-cli 0.164.0")
		return 0, true
	case "login status":
		if !c.signedIn {
			fmt.Fprintln(errOut, "Not logged in")
			return 1, true
		}
		fmt.Fprintln(out, "Logged in using ChatGPT")
		return 0, true
	case "plugin list --json":
		fmt.Fprint(out, codexPluginList(c.userPlugin))
		return 0, true
	case "plugin marketplace add dinoderek/engine", "plugin marketplace upgrade engine", "plugin add engine@engine":
		b.mutate("operator: " + args)
		if c.userMarketFail {
			fmt.Fprint(errOut, "marketplace unreachable")
			return 1, true
		}
		if strings.HasPrefix(args, "plugin add") {
			c.userPlugin = c.installed()
		}
		return 0, true
	case "app-server daemon version":
		if !c.operatorDaemon {
			fmt.Fprint(errOut, "Error: failed to connect to the socket")
			return 1, true
		}
		fmt.Fprintln(out, `{"status":"running"}`)
		return 0, true
	case "remote-control --json":
		fmt.Fprint(out, c.operatorRC.Out)
		fmt.Fprint(errOut, c.operatorRC.ErrOut)
		return c.operatorRC.Code, true
	}
	return 0, false
}

// inEngine answers the commands in engine's Codex home.
func (c *codexState) inEngine(b *box, args string, out, errOut io.Writer) (int, bool) {
	switch args {
	case "login status":
		if !c.engineSignedIn() {
			fmt.Fprintln(errOut, "Not logged in")
			return 1, true
		}
		fmt.Fprintln(out, "Logged in using ChatGPT")
		return 0, true
	case "plugin list --json":
		fmt.Fprint(out, codexPluginList(c.plugin))
		return 0, true
	case "plugin marketplace add dinoderek/engine", "plugin marketplace upgrade engine", "plugin add engine@engine":
		b.mutate(args)
		if c.marketFail {
			fmt.Fprint(errOut, "marketplace unreachable")
			return 1, true
		}
		if strings.HasPrefix(args, "plugin add") {
			c.plugin = c.installed()
		}
		return 0, true
	case "app-server daemon version":
		if !c.daemonUp {
			fmt.Fprint(errOut, "Error: failed to connect to the socket")
			return 1, true
		}
		fmt.Fprintln(out, `{"status":"running","backend":"pid"}`)
		return 0, true
	}
	return 0, false
}

// start is the daemon start.
func (c *codexState) start(b *box, cmd string, errOut io.Writer) (int, bool) {
	b.mutate(cmd)
	if c.startFail {
		fmt.Fprint(errOut, "remote control failed")
		return 1, true
	}
	c.started++
	c.daemonUp, c.rcOn = true, !c.startNoRC
	if c.startExit1 {
		fmt.Fprint(errOut, "Starting app-server daemon with remote control enabled...\nError: Remote control is enabled on box but the connection is errored.")
		return 1, true
	}
	return 0, true
}

// installed is the version a plugin install leaves: the caller's, unless the
// case says otherwise.
func (c *codexState) installed() string {
	if c.leavesNothing {
		return ""
	}
	return cond(c.pluginLeaves != "", c.pluginLeaves, callerPlugin)
}

func codexPluginList(version string) string {
	list := []map[string]any{}
	if version != "" {
		list = append(list, map[string]any{"pluginId": pluginIDForTest, "version": version, "enabled": true})
	}
	data, _ := json.Marshal(map[string]any{"installed": list, "available": []any{}})
	return string(data)
}

func cond(ok bool, yes, no string) string {
	if ok {
		return yes
	}
	return no
}
