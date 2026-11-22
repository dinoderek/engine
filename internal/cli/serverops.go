package cli

import (
	"context"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/controlcmd"
	"github.com/dinoderek/engine/internal/remote"
	"github.com/dinoderek/engine/internal/serverops"
	"github.com/dinoderek/engine/internal/watch"
)

// answerTimeout bounds each call that asks whether the server answers, so a
// hung server reads as one failure rather than a stuck command.
const answerTimeout = 10 * time.Second

// runServerOps runs `engine server status|restart`.
func runServerOps(args []string, env Env) int {
	exec := env.SSH
	if exec == nil {
		exec = remote.ExecCommand
	}
	sleep := env.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	d := serverops.Deps{
		Home: env.Home, Exec: exec, Sleep: sleep, DBPath: controlcmd.ServerDBPath,
		Answers: func() error { return answers(env) },
	}
	return serverops.Main(args, d, env.Out, env.Err)
}

// answers makes one bounded authenticated call to the server.
func answers(env Env) error {
	client, err := controlcmd.Connect(controlEnv(env))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), answerTimeout)
	defer cancel()
	_, err = client.Call(ctx, control.Request{Op: "list", State: "repo"})
	return err
}

// runWatch runs `engine watch` until it is interrupted.
func runWatch(args []string, env Env) int {
	return watch.Main(args, watch.Env{
		Home: env.Home, Err: env.Err, Now: env.Now, TelegramHTTP: env.TelegramHTTP,
		Connect: func() (watch.Caller, error) { return controlcmd.Connect(controlEnv(env)) },
	})
}
