package setup

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// codexHostsTOML lists codex on both hosts, so setup runs the Codex steps.
var codexHostsTOML = strings.NewReplacer(
	`harnesses = ["claude"]`, `harnesses = ["claude", "codex"]`,
	`bin = { claude = "~/bin/claude" }`, `bin = { claude = "~/bin/claude", codex = "~/.local/bin/codex" }`,
	`[host.mac]`, "[harness.codex]\nmax_sessions = 2\nlaunch = \"{bin} -m {model}\"\n"+
		"tiers.deep     = { model = \"sol\", effort = \"high\" }\n"+
		"tiers.standard = { model = \"sol\", effort = \"medium\" }\n"+
		"tiers.light    = { model = \"luna\", effort = \"medium\" }\n\n[host.mac]",
).Replace(hostsTOML)

// codexState is a host's Codex: the pinned binary, the operator's login, and
// what is in engine's Codex home. Its commands are the ones setup runs.
type codexState struct {
	home, userHome string
	bin            string // where the binary runs; "" is nowhere
	onPath         string // what `command -v codex` finds when bin is absent
	signedIn       bool   // the operator's Codex
	userAuth       bool   // the operator's auth.json is a file
	operatorRC     result // `codex remote-control --json` in the operator's home

	config     *string // engine's config.toml; nil is none
	authLink   string  // "", "FILE", or the link's target
	plugin     string  // version of engine's plugin in engine's home; "" is none
	marketFail bool
	// pluginLeaves is the version an install leaves, when not the caller's;
	// leavesNothing makes it leave none.
	pluginLeaves  string
	leavesNothing bool
	writeFail     bool
	// loginBroken makes engine's home reject the login it links.
	loginBroken bool
	daemonUp    bool
	remote      string // the state engine's remote control reports
	startFail   bool
	startLeaves string // remote control state after a start; "" is connected
	started     int
}

func (c *codexState) engineHome() string { return c.home + "/.local/share/engine/codex" }

// settledCodex is a host with Codex fully set up.
func settledCodex(home string) *codexState {
	c := &codexState{home: home, userHome: home + "/.codex", bin: home + "/.local/bin/codex", signedIn: true, userAuth: true,
		operatorRC: result{out: `{"status":"connected"}`}, plugin: callerPlugin, daemonUp: true, remote: "connected"}
	cfg := withEngineBlock("[marketplaces.engine]\nsource_type = \"git\"\n", engineBlock(home))
	c.config, c.authLink = &cfg, c.userHome+"/auth.json"
	return c
}

// freshCodex is a host with Codex installed and signed in, and nothing of
// engine's yet.
func freshCodex(home string) *codexState {
	c := settledCodex(home)
	c.config, c.authLink, c.plugin, c.daemonUp, c.remote = nil, "", "", false, ""
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
	case strings.HasPrefix(cmd, `t="$(cat `):
		return c.start(b, cmd, errOut)
	}
	if args, found := strings.CutPrefix(cmd, "CODEX_HOME="+eh+" "+c.bin+" "); found {
		return c.inEngine(b, args, out, errOut)
	}
	if args, found := strings.CutPrefix(cmd, c.bin+" "); found && c.bin != "" {
		return c.operator(args, out, errOut)
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
func (c *codexState) operator(args string, out, errOut io.Writer) (int, bool) {
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
	case "remote-control --json":
		fmt.Fprint(out, c.operatorRC.out)
		fmt.Fprint(errOut, c.operatorRC.errOut)
		return c.operatorRC.code, true
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
			c.plugin = cond(c.pluginLeaves != "", c.pluginLeaves, callerPlugin)
			if c.leavesNothing {
				c.plugin = ""
			}
		}
		return 0, true
	case "app-server daemon version":
		if !c.daemonUp {
			fmt.Fprint(errOut, "Error: failed to connect to the socket")
			return 1, true
		}
		fmt.Fprintln(out, `{"status":"running","backend":"pid"}`)
		return 0, true
	case "remote-control --json":
		return c.engineRemote(out, errOut), true
	}
	return 0, false
}

func (c *codexState) engineRemote(out, errOut io.Writer) int {
	switch c.remote {
	case "":
		fmt.Fprint(errOut, "Error: no daemon")
		return 1
	case remoteErrored:
		fmt.Fprint(errOut, "Error: Remote control is enabled on box but the connection is errored.")
		return 1
	}
	fmt.Fprintf(out, `{"status":%q}`, c.remote)
	return 0
}

// start is the daemon start. It must read the token from its file and never
// carry it on the command line.
func (c *codexState) start(b *box, cmd string, errOut io.Writer) (int, bool) {
	b.mutate(cmd)
	if c.startFail {
		fmt.Fprint(errOut, "cat: github-token: No such file or directory")
		return 1, true
	}
	c.started++
	c.daemonUp, c.remote = true, "connected"
	if c.startLeaves != "" {
		c.remote = c.startLeaves
	}
	if c.remote == remoteErrored {
		fmt.Fprint(errOut, "Starting app-server daemon with remote control enabled...\nError: Remote control is enabled on box but the connection is errored.")
		return 1, true
	}
	return 0, true
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
