package cli

import (
	"fmt"
	"slices"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/controlcmd"
)

func controlEnv(e Env) controlcmd.Env {
	var server *controlcmd.ServerDeps
	if e.SSH != nil || e.Sleep != nil {
		d := controlcmd.RealServerDeps()
		if e.SSH != nil {
			d.Exec = e.SSH
		}
		if e.Sleep != nil {
			d.Sleep = e.Sleep
		}
		server = &d
	}
	return controlcmd.Env{In: e.In, Out: e.Out, Err: e.Err, Home: e.Home, Getenv: e.Getenv, Server: server, Attach: func(args []string) error {
		code, err := e.Stream(nil, Streams{In: e.In, Out: e.Out, Err: e.Err}, args[0], args[1:]...)
		if err != nil {
			return err
		}
		if code != 0 {
			return fmt.Errorf("attach exited %d", code)
		}
		return nil
	}}
}

// removedUsage is what a form that named a GitHub issue, a task by its repo,
// or a task another way than its server ID gets: the server form.
var removedUsage = map[string]string{
	"run":    "usage: engine run <plan-id>  (p<n>)\n",
	"ps":     "usage: engine ps [--json]  (lists server tasks)\n",
	"attach": "usage: engine attach <task-id>  (t<n>)\n",
	"tell":   "usage: engine tell <task-id> <text...|->  (t<n>)\n",
	"lint":   "usage: engine lint <plan-id>  (p<n>)\n",
}

// serverCommand reports whether args are a server command: one of the
// server's own verbs, or run, tell, attach, lint or ps in their server form,
// which take a plan or task ID (ps takes none).
func serverCommand(args []string) bool {
	if slices.Contains([]string{"serve", "repo", "plan", "task", "updates", "inbox", "ask", "reply", "ack", "resolve", "merge-queue", "attempt"}, args[0]) {
		return true
	}
	if args[0] == "ps" {
		return !slices.Contains(args[1:], "--github")
	}
	return len(args) > 1 && slices.Contains([]string{"run", "tell", "attach", "lint"}, args[0]) && control.Number(args[1]) > 0 && (args[1][0] == 'p' || args[1][0] == 't')
}

// serverMergeRefusal tells a server executor how its PR merges instead.
const serverMergeRefusal = "a server session never merges: the coordinator merges on the task's host. " +
	"Run engine task merge-ready <task> --pr <url> --head <reviewed-sha> --method <squash|merge|rebase> --verdict APPROVED, then end your turn: the merge queue wakes you with its update"
