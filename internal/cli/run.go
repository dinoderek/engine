package cli

import (
	"fmt"
	"time"

	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/ps"
	"github.com/dinoderek/engine/internal/run"
	"github.com/dinoderek/engine/internal/start"
	"github.com/dinoderek/engine/internal/tracker"
)

const runUsage = "usage: engine run <owner/repo#n>\n"

// runRun coordinates a feature, or one task, on this host until it is over.
func runRun(args []string, env Env, sleep func(time.Duration)) int {
	if len(args) != 1 {
		fmt.Fprint(env.Err, runUsage)
		return 2
	}
	ref, err := tracker.ParseRef(args[0])
	if err != nil {
		fmt.Fprintln(env.Err, "engine run:", err)
		return 2
	}
	sys, taskOnly, err := runSystem(ref, env)
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
func runSystem(ref tracker.Ref, env Env) (run.System, bool, error) {
	token, err := tracker.Token(env.Getenv, env.Home)
	if err != nil {
		return run.System{}, false, err
	}
	_, host, err := hosts.Open(hosts.ConfigDir(env.Home))
	if err != nil {
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
	src := psSource(env, host, token)
	return run.System{
		Gh: env.Exec, Token: token, Home: env.Home, Host: host, Tmux: env.Tmux,
		ListAgents: agents(env, host),
		ListRows: func() ([]ps.Row, error) {
			l, err := ps.List(src)
			return l.Rows, err
		},
		Starts: start.Deps{GH: env.Exec, Run: env.Run, Tmux: env.Tmux, Home: env.Home, Getenv: env.Getenv, Now: env.Now},
		Out:    env.Out, Clock: env.Now,
	}, taskOnly, nil
}
