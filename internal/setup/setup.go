// Package setup is `engine setup [--host <name>]`: it brings a host to the
// caller's engine commit, plugin and config, in seven steps, and a host that
// lists codex also gets Codex's five. It is safe to rerun. It changes only
// engine's own files (~/.config/engine, ~/.local/bin, ~/.local/share/engine),
// Claude's plugin, and Codex's plugin and daemon in engine's own Codex home,
// and for anything else it prints the exact command for the operator to run.
// With no host it checks this one; the Codex steps also act on it.
package setup

import (
	"fmt"
	"io"
	"strings"

	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/remote"
	"github.com/dinoderek/engine/internal/tracker"
	"github.com/dinoderek/engine/internal/version"
)

// Deps are the outside world: the caller's build, the commands it runs, and
// the preflight check. Tests replace each one.
type Deps struct {
	Home   string
	Getenv func(string) string
	// Exec runs ssh, and sh for this host's own checks.
	Exec remote.Exec
	// GH runs gh, to confirm the caller's commit is on GitHub.
	GH tracker.Runner
	// Info is the caller's own build.
	Info version.Info
	// Check runs `engine start --check` for the harness ("" is the default
	// one) on the host being set up, prints its report, and reports whether an
	// unattended session there would run.
	Check func(harness string, out io.Writer) (bool, error)
}

// state is how a step ended.
type state string

const (
	stateOK      state = "ok"
	stateChanged state = "changed"
	stateOpen    state = "OPEN"
)

// outcome is what one step found or did. An open step carries the exact
// command that closes it, for the operator to run.
type outcome struct {
	state   state
	detail  string
	command string
}

func passed(detail string) outcome  { return outcome{state: stateOK, detail: detail} }
func changed(detail string) outcome { return outcome{state: stateChanged, detail: detail} }
func open(detail, command string) outcome {
	return outcome{state: stateOpen, detail: detail, command: command}
}

type step struct {
	n    int // its number in the report; a host checking itself skips some
	name string
	run  func() (outcome, error)
}

// runner is one setup run against one target.
type runner struct {
	d    Deps
	t    target
	want wanted
	// host is the target's entry in hosts.toml.
	host hosts.Host
	// defaultHarness is hosts.toml's, which a check with no harness runs.
	defaultHarness string
	// codex is set when the host lists codex, and holds what its steps find.
	codex *codexTarget
	// missing are the tools step 1 did not find, which later steps need.
	missing map[string]bool
	// opened counts the steps so far that ended open.
	opened int
}

// refuse is a setup that stopped before changing anything.
func refuse(format string, args ...any) error {
	return fmt.Errorf(format+"; nothing was changed", args...)
}

// Run sets up host, which is this one when empty or its own name, and reports
// whether every item is closed. A host that cannot be set up, or reached, is
// an error. The steps print as they finish, then the commands still to run.
func Run(host string, d Deps, out io.Writer) (bool, error) {
	cfg, local, err := hosts.Open(hosts.ConfigDir(d.Home))
	if err != nil {
		return false, refuse("%v", err)
	}
	t, h := target{name: local.Name, exec: d.Exec}, local
	if host != "" && host != local.Name {
		other, found := cfg.Hosts[host]
		if !found {
			return false, refuse("host %q is not in hosts.toml; its machines are %s", host, strings.Join(cfg.HostNames(), ", "))
		}
		t, h = target{name: other.Name, address: other.Address, exec: d.Exec}, other
	}
	r := &runner{d: d, t: t, host: h, defaultHarness: cfg.DefaultHarness, missing: map[string]bool{}}
	if h.Runs(codexHarness) {
		r.codex, r.t.acts = &codexTarget{}, true
	}
	if t.remote() {
		if r.want, err = r.resolveWanted(); err != nil {
			return false, err
		}
	}
	fmt.Fprintf(out, "engine setup: %s\n", r.t.label())
	var opens []string
	for _, s := range r.steps(out) {
		res, err := s.run()
		if err != nil {
			return false, err
		}
		line := fmt.Sprintf("%d. %s: %s", s.n, s.name, res.state)
		if res.detail != "" {
			line += " (" + res.detail + ")"
		}
		fmt.Fprintln(out, line)
		if res.state == stateOpen {
			r.opened++
			opens = append(opens, fmt.Sprintf("%d. %s: %s", s.n, s.name, res.command))
		}
	}
	return report(out, r.t, opens), nil
}

// report prints the commands still to run, and whether there are none.
func report(out io.Writer, t target, opens []string) bool {
	if len(opens) == 0 {
		fmt.Fprintf(out, "%s is set up\n", t.name)
		return true
	}
	fmt.Fprintf(out, "%d open item(s); run each command, then rerun engine setup%s:\n", len(opens), t.flag())
	for _, o := range opens {
		fmt.Fprintln(out, "  "+o)
	}
	return false
}

// steps lists the steps for the target. A host that is this one has no
// engine to install or config to copy: it only checks. A host that lists
// codex gets Codex's steps in either case, and the check runs last.
func (r *runner) steps(out io.Writer) []step {
	steps := []step{{1, "tools", r.tools}, {2, "engine", r.enginePath}, {3, "plugin", r.pluginInstalled},
		{5, "sign-in", r.signIn}, {6, "no suspend", r.noSuspend}}
	if r.t.remote() {
		steps = []step{
			{1, "tools", r.tools}, {2, "engine", r.installEngine}, {3, "plugin", r.installPlugin},
			{4, "config", r.copyConfig}, {5, "sign-in", r.signIn}, {6, "no suspend", r.noSuspend},
		}
	}
	last := 7
	if r.codex != nil {
		steps = append(steps, step{7, "codex", r.codexBinary}, step{8, "codex sign-in", r.codexSignIn},
			step{9, "codex home", r.codexHome}, step{10, "codex plugin", r.codexPluginStep}, step{11, "codex daemon", r.codexDaemon})
		last = 12
	}
	return append(steps, step{last, "preflight", func() (outcome, error) { return r.preflight(out) }})
}
