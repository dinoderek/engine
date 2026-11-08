package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/controlcmd"
	"github.com/dinoderek/engine/internal/hook"
	"github.com/dinoderek/engine/internal/tracker"
)

func controlEnv(e Env) controlcmd.Env {
	return controlcmd.Env{In: e.In, Out: e.Out, Err: e.Err, Home: e.Home, Getenv: e.Getenv, Attach: func(args []string) error {
		code, err := e.Stream(nil, tracker.Streams{In: e.In, Out: e.Out, Err: e.Err}, args[0], args[1:]...)
		if err != nil {
			return err
		}
		if code != 0 {
			return fmt.Errorf("attach exited %d", code)
		}
		return nil
	}}
}

func serverCommand(args []string) bool {
	if slices.Contains([]string{"serve", "repo", "plan", "task", "updates", "inbox", "ask", "reply", "ack", "resolve", "lock", "attempt"}, args[0]) {
		return true
	}
	if args[0] == "ps" {
		return !slices.Contains(args[1:], "--github")
	}
	return len(args) > 1 && slices.Contains([]string{"run", "tell", "start", "attach", "lint"}, args[0]) && control.Number(args[1]) > 0 && (args[1][0] == 'p' || args[1][0] == 't')
}

func preToolUse(e Env) int {
	raw, err := io.ReadAll(e.In)
	if err != nil {
		fmt.Fprintln(e.Err, "engine hook:", err)
		return 1
	}
	if code := hook.PreToolUse(bytes.NewReader(raw), e.Err); code != 0 {
		return code
	}
	var event struct {
		ToolName string `json:"tool_name"`
		Input    struct {
			Command string `json:"command"`
		} `json:"tool_input"`
	}
	if err = json.Unmarshal(raw, &event); err != nil || event.ToolName != "Bash" || !hook.IsMerge(event.Input.Command) {
		return 0
	}
	e.In = bytes.NewReader(raw)
	run, _ := findHookRun(e)
	server := e.Getenv("ENGINE_SERVER_URL") != ""
	if run.dir != "" {
		// Fail closed: only a session file known to be absent allows a merge.
		_, statErr := os.Stat(filepath.Join(run.dir, "server-session.json"))
		server = server || !os.IsNotExist(statErr)
	}
	if server {
		fmt.Fprintln(e.Err, "engine merge guard: "+serverMergeRefusal)
		return 2
	}
	return 0
}

// serverMergeRefusal tells a server executor how its PR merges instead.
const serverMergeRefusal = "a server session never merges: the coordinator merges on the task's host. " +
	"Holding the merge lock, run: engine task merge-ready <task> --pr <url> --head <reviewed-sha> --method <squash|merge|rebase> --verdict APPROVED, then wait for its update"
