package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/remote"
	"github.com/dinoderek/engine/internal/run"
	"github.com/dinoderek/engine/internal/tracker"
)

const runUsage = "usage: engine run <owner/repo#n> [--harness-ratio <h>=<n>,…]\n"

// runRun coordinates a feature, or one task, across the hosts until it is
// over.
func runRun(args []string, env Env, sleep func(time.Duration)) int {
	ref, ratio, err := parseRun(args)
	if err != nil {
		fmt.Fprintf(env.Err, "engine run: %v\n%s", err, runUsage)
		return 2
	}
	sys, taskOnly, err := runSystem(ref, env, ratio)
	if err != nil {
		fmt.Fprintln(env.Err, "engine run:", err)
		return 1
	}
	if err := run.Loop(sys, ref, taskOnly, sleep); err != nil {
		fmt.Fprintln(env.Err, "engine run:", err)
		return 1
	}
	return 0
}

// runSystem builds the World for ref, and says whether ref is one task. A
// task with an open blocker is refused.
func runSystem(ref tracker.Ref, env Env, ratio map[string]int) (run.System, bool, error) {
	token, err := tracker.Token(env.Getenv, env.Home)
	if err != nil {
		return run.System{}, false, err
	}
	cfg, host, err := hosts.Open(hosts.ConfigDir(env.Home))
	if err != nil {
		return run.System{}, false, err
	}
	if ratio != nil {
		cfg.HarnessRatio = ratio
	}
	if err := cfg.ValidateRatio(cfg.HarnessRatio); err != nil {
		return run.System{}, false, err
	}
	issue, err := tracker.ReadTask(env.Exec, token, ref)
	if err != nil {
		return run.System{}, false, err
	}
	taskOnly := !issue.Has("engine:feature")
	if taskOnly && len(issue.OpenBlockers) > 0 {
		return run.System{}, false, fmt.Errorf("%s is blocked by open %v", ref, issue.OpenBlockers)
	}
	starts := startDeps(env)
	return run.System{
		Gh: env.Exec, Token: token, Home: env.Home, Config: cfg, Host: host,
		Client: func(h hosts.Host) remote.Client { return starts.HostClient(h, h.Name != host.Name) },
		Claims: psSource(env, host, token),
		Starts: starts,
		Out:    env.Out, Clock: env.Now,
	}, taskOnly, nil
}

// parseRun accepts the ratio replacement before or after the reference.
func parseRun(args []string) (tracker.Ref, map[string]int, error) {
	var ref string
	var ratio map[string]int
	for i := 0; i < len(args); i++ {
		name, value, inline := strings.Cut(args[i], "=")
		if name == "--harness-ratio" {
			if ratio != nil {
				return tracker.Ref{}, nil, fmt.Errorf("--harness-ratio repeated")
			}
			if !inline && i+1 < len(args) {
				i++
				value = args[i]
			}
			parsed, err := hosts.ParseRatio(value)
			if err != nil {
				return tracker.Ref{}, nil, err
			}
			ratio = parsed
		} else if strings.HasPrefix(name, "-") || ref != "" {
			return tracker.Ref{}, nil, fmt.Errorf("unexpected argument %q", args[i])
		} else {
			ref = args[i]
		}
	}
	r, err := tracker.ParseRef(ref)
	return r, ratio, err
}
