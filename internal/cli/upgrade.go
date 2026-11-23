package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/controlcmd"
	"github.com/dinoderek/engine/internal/remote"
	"github.com/dinoderek/engine/internal/upgrade"
)

const upgradeUsage = "usage: engine upgrade [--dry-run]\n"

// runUpgrade brings every host to the head of main, or with --dry-run prints
// the steps. It exits 1 while any item is open, and 2 on bad flags. An
// interrupt ends the run early, but still starts the server it stopped.
func runUpgrade(args []string, env Env) int {
	dryRun := false
	switch {
	case len(args) == 1 && args[0] == "--dry-run":
		dryRun = true
	case len(args) != 0:
		fmt.Fprintf(env.Err, "engine upgrade: unknown arguments %q\n%s", args, upgradeUsage)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	closed, err := upgrade.Run(dryRun, upgradeDeps(env, ctx), env.Out)
	if err != nil {
		fmt.Fprintln(env.Err, "engine upgrade:", err)
		return 1
	}
	return checkCode(closed)
}

// upgradeDeps are what the upgrade runs with: env's runners, the server
// through the operator's client, and env's clock.
func upgradeDeps(env Env, ctx context.Context) upgrade.Deps {
	exec := env.SSH
	if exec == nil {
		exec = remote.ExecCommand
	}
	sleep := env.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	return upgrade.Deps{
		Home: env.Home, Exec: exec, GH: env.Run, Harnesses: adapters(env), Now: env.Now, Sleep: sleep, Context: ctx,
		Answers: func() error { return serverAnswers(env) },
		Mark:    func(end bool) error { return markUpgrade(env, end) },
	}
}

// serverAnswers lists the server's tasks, a call every server version
// answers. An interrupt does not cancel it: the wait for the restarted server
// still needs it.
func serverAnswers(env Env) error {
	client, err := controlcmd.Connect(controlEnv(env))
	if err != nil {
		return err
	}
	_, err = client.Call(context.Background(), control.Request{Op: "list", State: "task"})
	return err
}

// markUpgrade records the upgrade's start, or with end its end, on the
// server, under a request key of its own. An interrupt does not cancel it.
func markUpgrade(env Env, end bool) error {
	client, err := controlcmd.Connect(controlEnv(env))
	if err != nil {
		return err
	}
	stage := control.UpgradeStart
	if end {
		stage = control.UpgradeEnd
	}
	_, err = client.Call(context.Background(), control.Request{Op: "upgrade", State: stage, Key: "upgrade-" + stage + "-" + control.Secret()})
	return err
}
