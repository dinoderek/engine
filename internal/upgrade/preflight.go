package upgrade

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/remote"
	"github.com/dinoderek/engine/internal/setup"
	"github.com/dinoderek/engine/internal/version"
)

// survey is the preflight, which changes nothing: the fleet from hosts.toml,
// the server's host from config.toml, its unit answering is-active, no busy
// task, and each host's engine as it is.
func survey(d Deps) (*fleet, error) {
	dir := hosts.ConfigDir(d.Home)
	cfg, caller, err := hosts.Open(dir)
	if err != nil {
		return nil, refuse("%v", err)
	}
	s, err := hosts.ReadServer(dir)
	switch {
	case err != nil:
		return nil, refuse("%v", err)
	case s.Host == "":
		return nil, refuse("config.toml's [server] names no host, so upgrade cannot tell which host runs the server")
	}
	server, ok := cfg.Hosts[s.Host]
	switch {
	case !ok:
		return nil, refuse("config.toml's server host %q is not in hosts.toml", s.Host)
	case !server.Services.Recorded():
		return nil, refuse("host %s, the server's host, records no [host.%s.services] table, so upgrade cannot stop and start its server; upgrade it by hand", server.Name, server.Name)
	}
	f := &fleet{d: d, caller: caller, server: server, results: map[string]*hostResult{}}
	for _, name := range cfg.HostNames() {
		f.hosts = append(f.hosts, cfg.Hosts[name])
	}
	if err := f.checkServer(); err != nil {
		return nil, err
	}
	if err := f.checkTasks(); err != nil {
		return nil, err
	}
	for _, h := range f.hosts {
		f.results[h.Name] = f.readBefore(h)
	}
	if r := f.results[server.Name]; r.unreachable != "" {
		return nil, refuse("cannot read the engine on %s, the server's host: %s", server.Name, r.unreachable)
	}
	return f, nil
}

// checkServer asks systemd on the server's host whether the unit is active,
// which proves the host answers.
func (f *fleet) checkServer() error {
	line, err := f.server.ServerShellLine(hosts.ServerIsActive)
	if err != nil {
		return refuse("%v", err)
	}
	res, err := f.shell(f.server).Sh(line, "")
	if err != nil {
		return refuse("cannot reach %s, the server's host: %v", f.server.Name, err)
	}
	f.active = res.FirstLine()
	if f.active != "active" {
		// The server must run under the unit the upgrade stops and starts.
		start, _ := f.server.ServerShellLine(hosts.ServerStart)
		return refuse("the server's unit %s on %s is %s, not active; start it first (%s)", f.server.Services.Server, f.server.Name, harness.OrNone(f.active), f.shell(f.server).OnHost(start, false))
	}
	return nil
}

// checkTasks refuses while any task is running, in review or waiting on the
// operator, naming each.
func (f *fleet) checkTasks() error {
	tasks, err := f.d.Tasks()
	if err != nil {
		return refuse("could not list the server's tasks (unit %s is %s): %v", f.server.Services.Server, f.active, err)
	}
	var held []string
	for _, t := range tasks {
		if busy[t.State] {
			held = append(held, t.ID+" ("+t.State+")")
		}
	}
	if len(held) > 0 {
		slices.Sort(held)
		return refuse("%d task(s) are running, in review or waiting on the operator: %s; let them finish first", len(held), strings.Join(held, ", "))
	}
	return nil
}

// readBefore is h's engine before the upgrade, or why h cannot be reached.
func (f *fleet) readBefore(h hosts.Host) *hostResult {
	rev, err := f.engineOn(h)
	var unreachable *remote.UnreachableError
	switch {
	case errors.As(err, &unreachable):
		return &hostResult{before: "unknown", unreachable: unreachable.Error()}
	case err != nil:
		return &hostResult{before: "unknown", unreachable: err.Error()}
	}
	return &hostResult{before: rev}
}

// engineOn is the short commit of the engine on h's PATH, "-dirty" when
// built from a modified tree, or "none".
func (f *fleet) engineOn(h hosts.Host) (string, error) {
	res, err := f.shell(h).Sh("engine version", "")
	if err != nil || res.Code != 0 {
		return "none", err
	}
	info := version.Parse(res.Out)
	rev := short(info.Commit())
	if rev == "" {
		return "none", nil
	}
	if info.Dirty() {
		rev += "-dirty"
	}
	return rev, nil
}

// shortRev is how much of a commit the report shows, as setup does.
const shortRev = 7

func short(commit string) string {
	if len(commit) < shortRev {
		return ""
	}
	return commit[:shortRev]
}

// fullSHA is a full commit hash, the only thing read as the target.
var fullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

// resolveTarget reads the head of main once, and the Go its go.mod names.
func (f *fleet) resolveTarget() error {
	out, err := f.d.GH("", nil, "gh", "api", "repos/"+setup.EngineRepo+"/commits/main", "--jq", ".sha")
	if err != nil {
		return refuse("could not read the head of main (%s): %v", setup.EngineRepo, err)
	}
	commit := strings.TrimSpace(out)
	if !fullSHA.MatchString(commit) {
		return refuse("the head of main (%s) reads as %q, not a commit", setup.EngineRepo, commit)
	}
	toolchain, err := setup.ReadToolchain(f.d.GH, commit)
	if err != nil {
		return refuse("%v", err)
	}
	f.commit, f.toolchain = commit, toolchain
	return nil
}

// rest are the hosts set up after the server starts: the caller, then the
// others in name order.
func (f *fleet) rest() []hosts.Host {
	var out []hosts.Host
	if !f.local(f.server) {
		out = append(out, f.caller)
	}
	for _, h := range f.hosts {
		if h.Name != f.server.Name && !f.local(h) {
			out = append(out, h)
		}
	}
	return out
}

// shortTarget is the target as the report shows it.
func (f *fleet) shortTarget() string { return fmt.Sprintf("%s (%s)", short(f.commit), f.toolchain) }
