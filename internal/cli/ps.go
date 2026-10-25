package cli

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/ps"
	"github.com/dinoderek/engine/internal/remote"
	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/tracker"
)

const psUsage = "usage: engine ps [--json]\n"

// runPs lists every claimed task, asking each other host that holds a claim
// for its status. Warnings about probes that failed go to
// stderr and leave the exit code 0: the listing is still the best answer.
func runPs(args []string, env Env) int {
	if len(args) > 1 || len(args) == 1 && args[0] != "--json" {
		fmt.Fprint(env.Err, psUsage)
		return 2
	}
	listing, err := listClaimed(env)
	if err != nil {
		fmt.Fprintln(env.Err, "engine ps:", err)
		return 1
	}
	for _, w := range listing.Warnings {
		fmt.Fprintln(env.Err, "engine ps: warning:", w)
	}
	write := ps.WriteTable
	if len(args) == 1 {
		write = ps.WriteJSON
	}
	if err := write(env.Out, listing.Rows); err != nil {
		fmt.Fprintln(env.Err, "engine ps:", err)
		return 1
	}
	return 0
}

func listClaimed(env Env) (ps.Listing, error) {
	cfg, host, err := hosts.Open(hosts.ConfigDir(env.Home))
	if err != nil {
		return ps.Listing{}, err
	}
	token, err := tracker.Token(env.Getenv, env.Home)
	if err != nil {
		return ps.Listing{}, err
	}
	src := psSource(env, host, token)
	src.Remote = remoteStatus(env, cfg)
	return ps.List(src)
}

// remoteStatus asks another machine of hosts.toml for its status over SSH.
func remoteStatus(env Env, cfg *hosts.Config) func(string) (hostops.Status, error) {
	run := env.SSH
	if run == nil {
		run = remote.ExecCommand
	}
	return func(name string) (hostops.Status, error) {
		h, ok := cfg.Hosts[name]
		if !ok {
			return hostops.Status{}, fmt.Errorf("%q is not in hosts.toml", name)
		}
		return remote.SSH{Name: name, Address: h.Address, Exec: run}.Status()
	}
}

// psSource is where engine ps, and engine run's view of claims, read from.
func psSource(env Env, host hosts.Host, token string) ps.Source {
	tmux := &session.Tmux{Run: env.Local, Bin: "tmux"}
	return ps.Source{
		Claimed:  func() ([]tracker.ClaimedTask, error) { return tracker.Claimed(env.Exec, token) },
		Local:    host.Name,
		Windows:  tmux.Windows,
		Agents:   agents(env, host),
		Checkout: func(dir string) (session.Checkout, error) { return session.ProbeCheckout(env.Local, dir) },
		Now:      env.Now,
	}
}

// agents lists the live Claude Code sessions on host.
func agents(env Env, host hosts.Host) func() ([]session.Agent, error) {
	claude := expandHome(host.Bin["claude"], env.Home)
	return func() ([]session.Agent, error) {
		if claude == "" {
			return nil, fmt.Errorf("host %s has no claude binary in hosts.toml", host.Name)
		}
		return session.ListAgents(env.Local, claude)
	}
}

// expandHome turns a leading ~ into the home directory.
func expandHome(path, home string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[1:])
	}
	return path
}
