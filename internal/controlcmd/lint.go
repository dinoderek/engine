package controlcmd

import (
	"context"
	"fmt"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/controlrun"
	"github.com/dinoderek/engine/internal/lint"
)

func lintMain(args []string, e Env) int {
	if len(args) != 1 || len(args[0]) < 2 || args[0][0] != 'p' || control.Number(args[0]) == 0 {
		fmt.Fprintln(e.Err, "usage: engine lint <plan-id>")
		return 2
	}
	client, err := makeClient(e)
	if err != nil {
		fmt.Fprintln(e.Err, "engine lint:", err)
		return 1
	}
	ctx := e.Context
	if ctx == nil {
		ctx = context.Background()
	}
	// A single read returns a consistent, credential-scoped snapshot, including
	// dependencies outside the plan. No approval or coordination is required.
	out, err := client.Call(ctx, control.Request{Op: "plan-view", ID: args[0]})
	if err != nil {
		fmt.Fprintln(e.Err, "engine lint:", err)
		return 1
	}
	st := control.State{Items: map[string]control.Item{}}
	for _, item := range out.Items {
		st.Items[item.ID] = item
	}
	if st.Items[args[0]].Kind != "plan" {
		fmt.Fprintln(e.Err, "engine lint: unknown or inaccessible plan", args[0])
		return 1
	}
	tasks := controlrun.Tasks(st, args[0])
	problems := lint.Items(tasks)
	for _, p := range problems {
		fmt.Fprintln(e.Out, p)
	}
	if len(problems) > 0 {
		fmt.Fprintf(e.Out, "%s: %d problem(s)\n", args[0], len(problems))
		return 1
	}
	fmt.Fprintf(e.Out, "%s: the plan passes, %d tasks\n", args[0], len(tasks))
	return 0
}
