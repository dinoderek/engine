// Package setup is `engine setup [--host <name>]`: it brings a host to the
// caller's engine commit, plugin and config. Its own steps check the host's
// tools, engine, config and suspend; each harness the host lists adds its
// steps after them, through its adapter; gh's sign-in and the preflight
// check come last. The report numbers the steps in order. It is safe to
// rerun. It changes only engine's own files (~/.config/engine, ~/.local/bin,
// ~/.local/share/engine) and what the adapters change, and for anything else
// it prints the exact command for the operator to run. With no host it
// checks this one; an adapter's steps may still act on it.
package setup

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/remote"
	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/version"
)

// Deps are the outside world: the caller's build, the commands it runs, the
// harness adapters and the preflight check. Tests replace each one.
type Deps struct {
	Home   string
	Getenv func(string) string
	// Exec runs ssh, and sh for this host's own checks.
	Exec remote.Exec
	// GH runs gh, to confirm the caller's commit is on GitHub.
	GH session.RunFunc
	// Info is the caller's own build.
	Info version.Info
	// Harnesses are the adapters; a host gets the steps of those it lists.
	Harnesses harness.Adapters
	// Check runs `engine start --check` for the named harness on the host
	// being set up, prints its report, and reports whether an unattended
	// session there would run.
	Check func(harness string, out io.Writer) (bool, error)
}

// runner is one setup run against one target.
type runner struct {
	d    Deps
	t    target
	want wanted
	// host is the target's entry in hosts.toml.
	host hosts.Host
	// defaultHarness is the harness a request that names none runs.
	defaultHarness string
	// adapters are the adapters of the harnesses the host lists, in the
	// registry's order.
	adapters []harness.Adapter
	// names are the run's step names, in report order.
	names []string
	// missing are the tools the tools step did not find, which later steps
	// need.
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
	r.adapters = hostAdapters(d.Harnesses, h)
	if r.defaultHarness == "" {
		if a, ok := lookup(d.Harnesses, ""); ok {
			r.defaultHarness = a.Name()
		}
	}
	if t.remote() {
		if r.want, err = r.resolveWanted(); err != nil {
			return false, err
		}
	}
	steps := r.steps(out)
	fmt.Fprintf(out, "engine setup: %s\n", r.t.label())
	var opens []string
	for i, s := range steps {
		res, err := s.Run()
		if err != nil {
			return false, err
		}
		line := fmt.Sprintf("%d. %s: %s", i+1, s.Name, res.State)
		if res.Detail != "" {
			line += " (" + res.Detail + ")"
		}
		fmt.Fprintln(out, line)
		if res.State == harness.StepOpen {
			r.opened++
			opens = append(opens, fmt.Sprintf("%d. %s: %s", i+1, s.Name, res.Command))
		}
	}
	return report(out, r.t, opens), nil
}

// lookup is the adapter called name in set, which may be nil.
func lookup(set harness.Adapters, name string) (harness.Adapter, bool) {
	if set == nil {
		return nil, false
	}
	return set.Get(name)
}

// hostAdapters are the adapters of the harnesses h lists, in set's order.
func hostAdapters(set harness.Adapters, h hosts.Host) []harness.Adapter {
	if set == nil {
		return nil
	}
	var out []harness.Adapter
	for _, name := range set.Names() {
		if a, ok := set.Get(name); ok && h.Runs(name) {
			out = append(out, a)
		}
	}
	return out
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

// steps lists the steps for the target, and records their names and which
// harnesses act on this host. A host that is this one has no engine to
// install or config to copy: it only checks. Each harness the host lists
// adds its steps, in the registry's order. gh's sign-in comes next, and the
// check runs last.
func (r *runner) steps(out io.Writer) []harness.Step {
	steps := []harness.Step{{Name: "tools", Run: r.tools}, {Name: "engine", Run: r.enginePath}, {Name: "no suspend", Run: r.noSuspend}}
	if r.t.remote() {
		steps = []harness.Step{{Name: "tools", Run: r.tools}, {Name: "engine", Run: r.installEngine},
			{Name: "config", Run: r.copyConfig}, {Name: "no suspend", Run: r.noSuspend}}
	}
	for _, a := range r.adapters {
		own := a.Setup().Steps(stepTarget{r})
		if !r.t.remote() && slices.ContainsFunc(own, func(s harness.Step) bool { return s.Acts }) {
			r.t.acts = append(r.t.acts, a.Name())
		}
		steps = append(steps, own...)
	}
	steps = append(steps, harness.Step{Name: "gh auth", Run: r.ghAuth},
		harness.Step{Name: "preflight", Run: func() (harness.Outcome, error) { return r.preflight(out) }})
	r.names = make([]string, len(steps))
	for i, s := range steps {
		r.names[i] = s.Name
	}
	return steps
}
