package setup

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dinoderek/engine/internal/hosts"
)

// codexHarness is the harness a host lists to get the Codex steps.
const codexHarness = "codex"

// codexTarget is what the Codex steps know about a host that lists codex, and
// what the earlier ones found.
type codexTarget struct {
	bin      string // the pinned binary's absolute path
	word     string // bin as a shell word
	home     string // $HOME on the host
	userHome string // the operator's Codex home on the host
	// missing is set when step 7 found no usable binary; signedOut when step 8
	// found the operator not signed in.
	missing, signedOut bool
	// homeOpen is set when step 9 ended open.
	homeOpen bool
}

// engineHome is engine's own Codex home on the host.
func (c *codexTarget) engineHome() string { return c.home + "/" + hosts.CodexHomeDir }

// shellPath is the shell word for a file in dir.
func shellPath(dir, name string) string { return hosts.ShellWord(dir + "/" + name) }

// env is the prefix that points a command at engine's Codex home.
func (c *codexTarget) env() string { return "CODEX_HOME=" + hosts.ShellWord(c.engineHome()) + " " }

// run is a codex command in the operator's Codex home; inEngine is one in
// engine's.
func (c *codexTarget) run(args string) string      { return c.word + " " + args }
func (c *codexTarget) inEngine(args string) string { return c.env() + c.word + " " + args }

// codexBlocked is a step that needs the binary step 7 did not find.
func (r *runner) codexBlocked() outcome {
	return open("codex is not usable (step 7)", r.rerun())
}

// firstLine is the first non-empty line of the command's output, else of its
// errors.
func (res result) firstLine() string {
	for _, text := range []string{res.out, res.errOut} {
		if lines := (result{out: text}).lines(); len(lines) > 0 {
			return lines[0]
		}
	}
	return ""
}

// lastLine is the last non-empty line of the command's errors, else of its
// output: the line that says why, after any progress lines.
func (res result) lastLine() string {
	for _, text := range []string{res.errOut, res.out} {
		if lines := (result{out: text}).lines(); len(lines) > 0 {
			return lines[len(lines)-1]
		}
	}
	return fmt.Sprintf("exit status %d", res.code)
}

// codexBinary reads the host's paths, checks that the pinned Codex runs, and
// reports what is wrong with the operator's own Codex: a connection that is
// not up, and config keys it ignores.
func (r *runner) codexBinary() (outcome, error) {
	c := r.codex
	res, err := r.t.sh(`printf '%s\n%s\n' "$HOME" "${CODEX_HOME:-$HOME/.codex}"`, "")
	if err != nil {
		return outcome{}, err
	}
	if lines := res.lines(); res.code == 0 && len(lines) >= 2 {
		c.home, c.userHome = lines[0], lines[1]
	} else {
		c.missing = true
		return open("could not read the home directory: "+res.why(), r.rerun()), nil
	}
	bin, err := r.host.BinPath(codexHarness, c.home)
	if err != nil {
		c.missing = true
		return open(err.Error(), r.rerun()), nil
	}
	c.bin, c.word = bin, hosts.ShellWord(bin)
	res, err = r.t.sh(c.run("--version"), "")
	if err != nil {
		return outcome{}, err
	}
	if res.code != 0 {
		c.missing = true
		return r.codexAbsent(res)
	}
	notes, err := r.operatorNotes()
	if err != nil {
		return outcome{}, err
	}
	detail := strings.Join(append([]string{res.firstLine()}, notes...), "; ")
	return passed(detail), nil
}

// operatorNotes reads the operator's own Codex daemon, when one runs. Without
// one, `codex remote-control --json` does not read anything: it runs a server
// in the foreground, with remote control on, until it is stopped.
func (r *runner) operatorNotes() ([]string, error) {
	up, err := r.t.sh(r.codex.run("app-server daemon version"), "")
	if err != nil || up.code != 0 || !strings.Contains(up.out, `"running"`) {
		return nil, err
	}
	state, err := r.t.sh(r.codex.run("remote-control --json"), "")
	return operatorNotes(state), err
}

// codexAbsent is the open item for a binary that does not run: a link to the
// one on the PATH, else the install.
func (r *runner) codexAbsent(failed result) (outcome, error) {
	c := r.codex
	found, err := r.t.sh("command -v codex", "")
	if err != nil {
		return outcome{}, err
	}
	if elsewhere := found.firstLine(); found.code == 0 && elsewhere != c.bin {
		link := "mkdir -p " + hosts.ShellWord(filepath.Dir(c.bin)) + " && ln -sf " + hosts.ShellWord(elsewhere) + " " + c.word
		return open(fmt.Sprintf("codex is at %s, not at %s (hosts.toml)", elsewhere, c.bin), r.t.onHost(link, false)), nil
	}
	return open(fmt.Sprintf("codex does not run at %s (%s)", c.bin, failed.why()), "install Codex there (operator-setup.md), then "+r.rerun()), nil
}

// codexSignIn checks that the operator's Codex is signed in, which engine's
// home links to.
func (r *runner) codexSignIn() (outcome, error) {
	c := r.codex
	if c.missing {
		return r.codexBlocked(), nil
	}
	res, err := r.t.sh(c.run("login status"), "")
	if err != nil {
		return outcome{}, err
	}
	if res.code == 0 {
		return passed(res.firstLine()), nil
	}
	c.signedOut = true
	login := c.run("login")
	if r.t.remote() {
		login += " --device-auth"
	}
	return open("not signed in to Codex", r.t.onHost(login, true)), nil
}

// piece is one part of a step: what it changed, and the open item it ended
// on, if any.
type piece struct {
	did  string
	stop *outcome
}

func stopped(detail, command string) piece {
	o := open(detail, command)
	return piece{stop: &o}
}

// codexHome brings engine's Codex home to what a session there needs: its
// config.toml block and the operator's login linked in.
func (r *runner) codexHome() (outcome, error) {
	if r.codex.missing {
		return r.codexBlocked(), nil
	}
	var did []string
	for _, part := range []func() (piece, error){r.writeCodexConfig, r.linkCodexLogin} {
		p, err := part()
		if err != nil {
			return outcome{}, err
		}
		if p.stop != nil {
			r.codex.homeOpen = true
			return *p.stop, nil
		}
		if p.did != "" {
			did = append(did, p.did)
		}
	}
	if len(did) == 0 {
		return passed(r.codex.engineHome()), nil
	}
	return changed(strings.Join(did, ", ")), nil
}

// writeCodexConfig puts engine's block at the top of config.toml, keeping what
// Codex wrote after it, and writes the file only when it differs.
func (r *runner) writeCodexConfig() (piece, error) {
	c := r.codex
	file := shellPath(c.engineHome(), "config.toml")
	cur, err := r.t.sh("cat "+file, "")
	if err != nil {
		return piece{}, err
	}
	existing := ""
	if cur.code == 0 {
		existing = cur.out
	}
	want := withEngineBlock(existing, engineBlock(c.home))
	if cur.code == 0 && want == existing {
		return piece{}, nil
	}
	tmp := shellPath(c.engineHome(), "config.toml.tmp")
	res, err := r.t.sh("umask 077 && mkdir -p "+hosts.ShellWord(c.engineHome())+" && cat > "+tmp+" && mv "+tmp+" "+file, want)
	if err != nil {
		return piece{}, err
	}
	if res.code != 0 {
		return stopped("write config.toml failed: "+res.why(), r.rerun()), nil
	}
	return piece{did: "wrote config.toml"}, nil
}

// linkCodexLogin links the operator's auth.json into engine's home, so one
// sign-in serves both. A file already there is engine's own login and stays.
// Then it checks that engine's home is signed in.
func (r *runner) linkCodexLogin() (piece, error) {
	c := r.codex
	if c.signedOut || c.userHome == c.engineHome() {
		return piece{}, nil
	}
	source, link := shellPath(c.userHome, "auth.json"), shellPath(c.engineHome(), "auth.json")
	state, err := r.t.sh(fmt.Sprintf(`if [ -L %[1]s ]; then readlink %[1]s; elif [ -e %[1]s ]; then echo FILE; else echo NONE; fi`, link), "")
	if err != nil {
		return piece{}, err
	}
	did := ""
	if have := state.firstLine(); have != "FILE" && have != c.userHome+"/auth.json" {
		if p, err := r.makeLink(source, link); err != nil || p.stop != nil {
			return p, err
		}
		did = "linked the login"
	}
	login := c.inEngine("login")
	res, err := r.t.sh(c.inEngine("login status"), "")
	if err != nil {
		return piece{}, err
	}
	if res.code != 0 {
		return stopped("engine's Codex home is not signed in", r.t.onHost(login, true)), nil
	}
	return piece{did: did}, nil
}

// makeLink points link at the operator's login, which must be a file.
func (r *runner) makeLink(source, link string) (piece, error) {
	c := r.codex
	has, err := r.t.sh("[ -f "+source+" ]", "")
	if err != nil {
		return piece{}, err
	}
	if has.code != 0 {
		return stopped("the operator's login is not a file at "+c.userHome+"/auth.json, so engine's home has none to link",
			r.t.onHost(c.inEngine("login"), true)), nil
	}
	res, err := r.t.sh("umask 077 && mkdir -p "+hosts.ShellWord(c.engineHome())+" && ln -sfn "+source+" "+link, "")
	if err != nil {
		return piece{}, err
	}
	if res.code != 0 {
		return stopped("link the login failed: "+res.why(), r.rerun()), nil
	}
	return piece{}, nil
}

// engineCodexPlugin is the version of engine's plugin in engine's Codex home,
// and whether it is installed and enabled.
func (r *runner) engineCodexPlugin() (string, bool, error) {
	res, err := r.t.sh(r.codex.inEngine("plugin list --json"), "")
	if err != nil {
		return "", false, err
	}
	version, found := installedCodexPlugin(res.out)
	return version, found, nil
}

// codexPluginStep installs engine's plugin into engine's Codex home, and
// checks the operator's, which sessions still launch with and whose hooks
// report their state. Setup leaves the operator's home alone: it prints the
// install for the operator to run.
func (r *runner) codexPluginStep() (outcome, error) {
	c := r.codex
	if c.missing {
		return r.codexBlocked(), nil
	}
	res, err := r.installEnginePlugin()
	if err != nil || res.state == stateOpen {
		return res, err
	}
	list, err := r.t.sh(c.run("plugin list --json"), "")
	if err != nil {
		return outcome{}, err
	}
	if _, found := installedCodexPlugin(list.out); found {
		return res, nil
	}
	install := c.run("plugin marketplace add "+engineRepo) + " && " + c.run("plugin add "+pluginID)
	return open("the Codex plugin is not installed in the operator's Codex home, which sessions still launch with", r.t.onHost(install, false)), nil
}

// installEnginePlugin installs engine's plugin from this repo's marketplace
// into engine's Codex home, or brings it to the caller's version.
func (r *runner) installEnginePlugin() (outcome, error) {
	c := r.codex
	have, installed, err := r.engineCodexPlugin()
	if err != nil || (installed && atLeast(have, r.want.plugin)) {
		return passed(have), err
	}
	for _, args := range []string{"plugin marketplace add " + engineRepo, "plugin marketplace upgrade engine", "plugin add " + pluginID} {
		res, err := r.t.sh(c.inEngine(args), "")
		if err != nil {
			return outcome{}, err
		}
		if res.code != 0 {
			return open("codex "+args+" failed: "+res.why(), r.t.onHost(c.inEngine(args), false)), nil
		}
	}
	after, found, err := r.engineCodexPlugin()
	switch {
	case err != nil:
		return outcome{}, err
	case !found:
		return open(pluginID+" is not enabled in engine's Codex home after the install", r.rerun()), nil
	case !atLeast(after, r.want.plugin):
		return open(fmt.Sprintf("the marketplace gives Codex plugin %s there, older than this host's %s; merge the plugin change first", after, r.want.plugin), r.rerun()), nil
	}
	return changed(fmt.Sprintf("%s, was %s", after, orNone(have))), nil
}

// remoteOnCommand prints on when a process runs from the Codex home's packages
// with remote control on (the `--remote-control` argument), else off. The
// daemon's own `codex remote-control --json` does not tell: it is not per Codex
// home, and when it finds no daemon it serves in the foreground. It matches the process's own program
// (the first field), so the shell and grep that run it, whose arguments only
// mention the path, never match.
func remoteOnCommand(engineHome string) string {
	return fmt.Sprintf(`ps -ww -eo args= | awk -v p=%s 'index($1, p) == 1 && / --remote-control( |$)/ { on = 1 } END { print on ? "on" : "off" }'`,
		hosts.ShellWord(engineHome+"/packages/"))
}

// daemonState reads engine's daemon: whether it runs, and whether its process
// was started with remote control on.
func (r *runner) daemonState() (up, remoteOn bool, err error) {
	c := r.codex
	res, err := r.t.sh(c.inEngine("app-server daemon version"), "")
	if err != nil || res.code != 0 || !strings.Contains(res.out, `"running"`) {
		return false, false, err
	}
	ps, err := r.t.sh(remoteOnCommand(c.engineHome()), "")
	return true, ps.firstLine() == "on", err
}

// daemonStart is the command that starts engine's daemon with remote control
// on and the machine user's token in its environment. The token comes from
// its file, so it is on no command line.
func (r *runner) daemonStart() string {
	c := r.codex
	file := shellPath(hosts.ConfigDir(c.home), "github-token")
	return fmt.Sprintf(`t="$(cat %s)" && [ -n "$t" ] && GH_TOKEN="$t" %s`, file, c.inEngine("remote-control start"))
}

// codexDaemon makes sure engine's own app-server daemon runs from engine's
// Codex home with remote control on, and starts it when it does not.
func (r *runner) codexDaemon() (outcome, error) {
	if r.codex.missing {
		return r.codexBlocked(), nil
	}
	if r.codex.signedOut || r.codex.homeOpen {
		return open("engine's Codex home is not ready (steps 8 and 9)", r.rerun()), nil
	}
	up, on, err := r.daemonState()
	if err != nil {
		return outcome{}, err
	}
	if up && on {
		return passed("running; remote control on"), nil
	}
	// A start exits 1 when the connection to the apps is errored, though the
	// daemon runs with remote control on: judge it by what runs afterwards.
	start := r.daemonStart()
	res, err := r.t.sh(start, "")
	if err != nil {
		return outcome{}, err
	}
	if up, on, err = r.daemonState(); err != nil {
		return outcome{}, err
	}
	switch {
	case !up:
		return open("engine's daemon is not running after the start: "+res.lastLine(), r.t.onHost(start, false)), nil
	case !on:
		return open("remote control is off after the start: "+res.lastLine(), r.t.onHost(start, false)), nil
	}
	return changed("started; remote control on"), nil
}
