package cli

import (
	"fmt"

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
	if err := write(env.Out, listing); err != nil {
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
	src.Hosts = cfg.HostNames()
	src.Status = hostStatus(env, cfg, host.Name)
	return ps.List(src)
}

// hostStatus reads a host's status: this host's in process, another machine
// of hosts.toml's over SSH.
func hostStatus(env Env, cfg *hosts.Config, local string) func(string) (hostops.Status, error) {
	run := env.SSH
	if run == nil {
		run = remote.ExecCommand
	}
	return func(name string) (hostops.Status, error) {
		if name == local {
			return hostops.ReadStatus(hostDeps(env))
		}
		h, ok := cfg.Hosts[name]
		if !ok {
			return hostops.Status{}, fmt.Errorf("%q is not in hosts.toml", name)
		}
		return remote.SSH{Name: name, Address: h.Address, Exec: run}.Status()
	}
}

// psSource is where engine ps, and engine run's view of claims, read from.
// Engine run reads each host's status from its own replies.
func psSource(env Env, host hosts.Host, token string) ps.Source {
	return ps.Source{
		Claimed:  func() ([]tracker.ClaimedTask, error) { return tracker.Claimed(env.Exec, token) },
		Local:    host.Name,
		Checkout: func(dir string) (session.Checkout, error) { return session.ProbeCheckout(env.Local, dir) },
		Now:      env.Now,
	}
}
