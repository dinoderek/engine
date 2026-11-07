package controlcmd

import (
	"context"
	"fmt"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/remote"
)

func attachMain(args []string, e Env) int {
	if len(args) != 1 {
		fmt.Fprintln(e.Err, "usage: engine attach <task-id>")
		return 2
	}
	client, err := makeClient(e)
	if err == nil {
		err = attachTask(client, args[0], e)
	}
	if err != nil {
		fmt.Fprintln(e.Err, "engine attach:", err)
		return 1
	}
	return 0
}

func attachTask(client control.Client, id string, e Env) error {
	ctx := e.Context
	if ctx == nil {
		ctx = context.Background()
	}
	item, err := client.Call(ctx, control.Request{Op: "get", ID: id})
	if err != nil {
		return err
	}
	attempts, err := client.Call(ctx, control.Request{Op: "attempts", ID: id})
	if err != nil {
		return err
	}
	for _, at := range attempts.Attempts {
		if at.ID != item.Item.Attempt {
			continue
		}
		if at.Window == "" || at.State == "ended" {
			return fmt.Errorf("execution has no active window")
		}
		cfg, local, err := hosts.Open(hosts.ConfigDir(e.Home))
		if err != nil {
			return err
		}
		h, ok := cfg.Hosts[at.Host]
		if !ok {
			return fmt.Errorf("execution host is not configured")
		}
		command := []string{"tmux", "attach", "-t", "=engine:" + at.Window}
		if h.Name != local.Name {
			command = []string{"ssh", "-t", h.Address, remote.ShellJoin(command)}
		}
		if e.Attach == nil {
			return fmt.Errorf("interactive attach is unavailable")
		}
		return e.Attach(command)
	}
	return fmt.Errorf("task has no execution")
}
