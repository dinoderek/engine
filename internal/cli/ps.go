package cli

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/ps"
	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/tracker"
)

const psUsage = "usage: engine ps [--json]\n"

// runPs lists every claimed task. Warnings about probes that failed go to
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
	_, host, err := hosts.Open(hosts.ConfigDir(env.Home))
	if err != nil {
		return ps.Listing{}, err
	}
	token, err := tracker.Token(env.Getenv, env.Home)
	if err != nil {
		return ps.Listing{}, err
	}
	tmux := &session.Tmux{Run: env.Local, Bin: "tmux"}
	claude := expandHome(host.Bin["claude"], env.Home)
	return ps.List(ps.Source{
		Claimed: func() ([]tracker.ClaimedTask, error) { return tracker.Claimed(env.Exec, token) },
		Local:   host.Name,
		Windows: tmux.Windows,
		Agents: func() ([]session.Agent, error) {
			if claude == "" {
				return nil, fmt.Errorf("host %s has no claude binary in hosts.toml", host.Name)
			}
			return session.ListAgents(env.Local, claude)
		},
		Checkout: func(dir string) (session.Checkout, error) { return session.ProbeCheckout(env.Local, dir) },
		Now:      env.Now,
	})
}

// expandHome turns a leading ~ into the home directory.
func expandHome(path, home string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[1:])
	}
	return path
}
