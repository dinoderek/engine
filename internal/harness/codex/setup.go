package codex

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/hosts"
)

// setupRun is one setup's Codex steps on a host that runs Codex: what the
// earlier steps found, which the later ones read.
type setupRun struct {
	t        harness.Target
	bin      string // the pinned binary's absolute path
	word     string // bin as a shell word
	home     string // $HOME on the host
	userHome string // the operator's Codex home on the host
	// missing is set when the codex step found no usable binary; signedOut
	// when the sign-in step found the operator not signed in.
	missing, signedOut bool
	// homeOpen is set when the home step ended open.
	homeOpen bool
}

// Setup is Codex's part of engine setup: the pinned binary, the operator's
// sign-in, engine's own Codex home with its config, login link and plugin,
// engine's plugin in the operator's Codex home, and engine's remote-control
// daemon. The steps act on this host too. They change engine's Codex home,
// and the operator's only through Codex's own plugin commands.
func (Adapter) Setup() harness.Setup { return setup{} }

type setup struct{}

// Tools are none: hosts.toml pins the binary, which the codex step checks.
func (setup) Tools() []string { return nil }

// Plugin keeps none to read: the caller's plugin version comes from another
// harness.
func (setup) Plugin(harness.Shell) (string, bool, error) { return "", false, nil }

func (setup) Steps(t harness.Target) []harness.Step {
	r := &setupRun{t: t}
	return []harness.Step{
		{Name: "codex", Run: r.codexBinary},
		{Name: "codex sign-in", Run: r.codexSignIn},
		{Name: "codex home", Run: r.codexHome, Acts: true},
		{Name: "codex plugin", Run: r.codexPluginStep, Acts: true},
		{Name: "codex daemon", Run: r.codexDaemon, Acts: true},
	}
}

// CheckFailed: the check's own report says what stopped the session.
func (setup) CheckFailed(t harness.Target) (string, string) {
	command := "engine start --check --harness " + Name
	if t.Remote() {
		command += " --host " + t.Host().Name
	}
	return "an unattended Codex session there would stop; read the report above, then rerun", command
}

// PluginUpdate upgrades engine's marketplace in the operator's Codex home,
// then adds the plugin from it, with the binary hosts.toml pins for h.
func (setup) PluginUpdate(h hosts.Host, home string) ([]string, error) {
	bin, err := h.BinPath(Name, home)
	if err != nil {
		return nil, err
	}
	word := hosts.ShellWord(bin)
	return []string{word + " plugin marketplace upgrade engine", word + " plugin add " + harness.PluginID}, nil
}

// engineHome is engine's own Codex home on the host.
func (r *setupRun) engineHome() string { return r.home + "/" + HomeDir }

// shellPath is the shell word for a file in dir.
func shellPath(dir, name string) string { return hosts.ShellWord(dir + "/" + name) }

// env is the prefix that points a command at engine's Codex home.
func (r *setupRun) env() string { return "CODEX_HOME=" + hosts.ShellWord(r.engineHome()) + " " }

// run is a codex command in the operator's Codex home; inEngine is one in
// engine's.
func (r *setupRun) run(args string) string      { return r.word + " " + args }
func (r *setupRun) inEngine(args string) string { return r.env() + r.word + " " + args }

// codexBlocked is a step that needs the binary the codex step did not find.
func (r *setupRun) codexBlocked() harness.Outcome {
	return harness.Open("codex is not usable ("+r.t.Ref("codex")+")", r.t.Rerun())
}

// codexBinary reads the host's paths, checks that the pinned Codex runs, and
// reports what is wrong with the operator's own Codex: a connection that is
// not up, and config keys it ignores.
func (r *setupRun) codexBinary() (harness.Outcome, error) {
	res, err := r.t.Sh(`printf '%s\n%s\n' "$HOME" "${CODEX_HOME:-$HOME/.codex}"`, "")
	if err != nil {
		return harness.Outcome{}, err
	}
	if lines := res.Lines(); res.Code == 0 && len(lines) >= 2 {
		r.home, r.userHome = lines[0], lines[1]
	} else {
		r.missing = true
		return harness.Open("could not read the home directory: "+res.Why(), r.t.Rerun()), nil
	}
	bin, err := r.t.Host().BinPath(Name, r.home)
	if err != nil {
		r.missing = true
		return harness.Open(err.Error(), r.t.Rerun()), nil
	}
	r.bin, r.word = bin, hosts.ShellWord(bin)
	res, err = r.t.Sh(r.run("--version"), "")
	if err != nil {
		return harness.Outcome{}, err
	}
	if res.Code != 0 {
		r.missing = true
		return r.codexAbsent(res)
	}
	notes, err := r.operatorNotes()
	if err != nil {
		return harness.Outcome{}, err
	}
	detail := strings.Join(append([]string{res.FirstLine()}, notes...), "; ")
	return harness.Passed(detail), nil
}

// operatorNotes reads the operator's own Codex daemon, when one runs. Without
// one, `codex remote-control --json` does not read anything: it runs a server
// in the foreground, with remote control on, until it is stopped.
func (r *setupRun) operatorNotes() ([]string, error) {
	up, err := r.t.Sh(r.run("app-server daemon version"), "")
	if err != nil || up.Code != 0 || !strings.Contains(up.Out, `"running"`) {
		return nil, err
	}
	state, err := r.t.Sh(r.run("remote-control --json"), "")
	return operatorNotes(state), err
}

// codexAbsent is the open item for a binary that does not run: a link to the
// one on the PATH, else the install.
func (r *setupRun) codexAbsent(failed harness.Result) (harness.Outcome, error) {
	found, err := r.t.Sh("command -v codex", "")
	if err != nil {
		return harness.Outcome{}, err
	}
	if elsewhere := found.FirstLine(); found.Code == 0 && elsewhere != r.bin {
		link := "mkdir -p " + hosts.ShellWord(filepath.Dir(r.bin)) + " && ln -sf " + hosts.ShellWord(elsewhere) + " " + r.word
		return harness.Open(fmt.Sprintf("codex is at %s, not at %s (hosts.toml)", elsewhere, r.bin), r.t.OnHost(link, false)), nil
	}
	return harness.Open(fmt.Sprintf("codex does not run at %s (%s)", r.bin, failed.Why()), "install Codex there (operator-setup.md), then "+r.t.Rerun()), nil
}

// codexSignIn checks that the operator's Codex is signed in, which engine's
// home links to.
func (r *setupRun) codexSignIn() (harness.Outcome, error) {
	if r.missing {
		return r.codexBlocked(), nil
	}
	res, err := r.t.Sh(r.run("login status"), "")
	if err != nil {
		return harness.Outcome{}, err
	}
	if res.Code == 0 {
		return harness.Passed(res.FirstLine()), nil
	}
	r.signedOut = true
	login := r.run("login")
	if r.t.Remote() {
		login += " --device-auth"
	}
	return harness.Open("not signed in to Codex", r.t.OnHost(login, true)), nil
}

// piece is one part of a step: what it changed, and the open item it ended
// on, if any.
type piece struct {
	did  string
	stop *harness.Outcome
}

func stopped(detail, command string) piece {
	o := harness.Open(detail, command)
	return piece{stop: &o}
}

// codexHome brings engine's Codex home to what a session there needs: its
// config.toml block and the operator's login linked in.
func (r *setupRun) codexHome() (harness.Outcome, error) {
	if r.missing {
		return r.codexBlocked(), nil
	}
	var did []string
	for _, part := range []func() (piece, error){r.writeCodexConfig, r.linkCodexLogin} {
		p, err := part()
		if err != nil {
			return harness.Outcome{}, err
		}
		if p.stop != nil {
			r.homeOpen = true
			return *p.stop, nil
		}
		if p.did != "" {
			did = append(did, p.did)
		}
	}
	if len(did) == 0 {
		return harness.Passed(r.engineHome()), nil
	}
	return harness.Changed(strings.Join(did, ", ")), nil
}

// writeCodexConfig puts engine's block at the top of config.toml, keeping what
// Codex wrote after it, and writes the file only when it differs.
func (r *setupRun) writeCodexConfig() (piece, error) {
	file := shellPath(r.engineHome(), "config.toml")
	cur, err := r.t.Sh("cat "+file, "")
	if err != nil {
		return piece{}, err
	}
	existing := ""
	if cur.Code == 0 {
		existing = cur.Out
	}
	want := withEngineBlock(existing, engineBlock(r.home))
	if cur.Code == 0 && want == existing {
		return piece{}, nil
	}
	tmp := shellPath(r.engineHome(), "config.toml.tmp")
	res, err := r.t.Sh("umask 077 && mkdir -p "+hosts.ShellWord(r.engineHome())+" && cat > "+tmp+" && mv "+tmp+" "+file, want)
	if err != nil {
		return piece{}, err
	}
	if res.Code != 0 {
		return stopped("write config.toml failed: "+res.Why(), r.t.Rerun()), nil
	}
	return piece{did: "wrote config.toml"}, nil
}

// linkCodexLogin links the operator's auth.json into engine's home, so one
// sign-in serves both. A file already there is engine's own login and stays.
// Then it checks that engine's home is signed in.
func (r *setupRun) linkCodexLogin() (piece, error) {
	if r.signedOut || r.userHome == r.engineHome() {
		return piece{}, nil
	}
	source, link := shellPath(r.userHome, "auth.json"), shellPath(r.engineHome(), "auth.json")
	state, err := r.t.Sh(fmt.Sprintf(`if [ -L %[1]s ]; then readlink %[1]s; elif [ -e %[1]s ]; then echo FILE; else echo NONE; fi`, link), "")
	if err != nil {
		return piece{}, err
	}
	did := ""
	if have := state.FirstLine(); have != "FILE" && have != r.userHome+"/auth.json" {
		if p, err := r.makeLink(source, link); err != nil || p.stop != nil {
			return p, err
		}
		did = "linked the login"
	}
	login := r.inEngine("login")
	res, err := r.t.Sh(r.inEngine("login status"), "")
	if err != nil {
		return piece{}, err
	}
	if res.Code != 0 {
		return stopped("engine's Codex home is not signed in", r.t.OnHost(login, true)), nil
	}
	return piece{did: did}, nil
}

// makeLink points link at the operator's login, which must be a file.
func (r *setupRun) makeLink(source, link string) (piece, error) {
	has, err := r.t.Sh("[ -f "+source+" ]", "")
	if err != nil {
		return piece{}, err
	}
	if has.Code != 0 {
		return stopped("the operator's login is not a file at "+r.userHome+"/auth.json, so engine's home has none to link",
			r.t.OnHost(r.inEngine("login"), true)), nil
	}
	res, err := r.t.Sh("umask 077 && mkdir -p "+hosts.ShellWord(r.engineHome())+" && ln -sfn "+source+" "+link, "")
	if err != nil {
		return piece{}, err
	}
	if res.Code != 0 {
		return stopped("link the login failed: "+res.Why(), r.t.Rerun()), nil
	}
	return piece{}, nil
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
func (r *setupRun) daemonState() (up, remoteOn bool, err error) {
	res, err := r.t.Sh(r.inEngine("app-server daemon version"), "")
	if err != nil || res.Code != 0 || !strings.Contains(res.Out, `"running"`) {
		return false, false, err
	}
	ps, err := r.t.Sh(remoteOnCommand(r.engineHome()), "")
	return true, ps.FirstLine() == "on", err
}

// daemonDrops are what a session's window exports for its own run, which
// setup inherits when a session runs it. The daemon must not keep them: a
// joined session's hooks run in its environment, and would write to that run.
const daemonDrops = "ENGINE_RUN_DIR ENGINE_HARNESS ENGINE_UNATTENDED TMUX TMUX_PANE"

// daemonStart starts remote control without retaining a session's variables.
func (r *setupRun) daemonStart() string {
	return fmt.Sprintf("unset %s && %s", daemonDrops, r.inEngine("remote-control start"))
}

// codexDaemon makes sure engine's own app-server daemon runs from engine's
// Codex home with remote control on, and starts it when it does not.
func (r *setupRun) codexDaemon() (harness.Outcome, error) {
	if r.missing {
		return r.codexBlocked(), nil
	}
	if r.signedOut || r.homeOpen {
		return harness.Open("engine's Codex home is not ready ("+r.t.Ref("codex sign-in", "codex home")+")", r.t.Rerun()), nil
	}
	up, on, err := r.daemonState()
	if err != nil {
		return harness.Outcome{}, err
	}
	if up {
		ready, err := r.daemonEnvironmentReady()
		if err != nil {
			return harness.Outcome{}, err
		}
		if !ready {
			stop := r.t.OnHost(r.inEngine("app-server daemon stop"), false)
			return harness.Open("engine's daemon must restart to inherit the host environment; finish its sessions, then stop it and rerun setup", stop+" && "+r.t.Rerun()), nil
		}
		if on {
			instance, err := r.daemonInstance()
			if err != nil {
				return harness.Outcome{}, err
			}
			conn, err := r.remoteConnection(instance)
			if err != nil {
				return harness.Outcome{}, err
			}
			return harness.Passed("running; remote control on, " + conn), nil
		}
	}
	// A start exits 1 when the connection to the apps is errored, though the
	// daemon runs with remote control on: judge it by what runs afterwards.
	start := r.daemonStart()
	res, err := r.t.Sh(start, "")
	if err != nil {
		return harness.Outcome{}, err
	}
	if up, on, err = r.daemonState(); err != nil {
		return harness.Outcome{}, err
	}
	switch {
	case !up:
		return harness.Open("engine's daemon is not running after the start: "+res.LastLine(), r.t.OnHost(start, false)), nil
	case !on:
		return harness.Open("remote control is off after the start: "+res.LastLine(), r.t.OnHost(start, false)), nil
	}
	return r.recordDaemonEnvironment()
}
