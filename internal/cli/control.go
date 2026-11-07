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

func serverCommand(args []string, e Env) bool {
	if slices.Contains([]string{"serve", "repo", "plan", "task", "updates", "inbox", "ask", "reply", "ack", "resolve", "lock", "attempt"}, args[0]) {
		return true
	}
	if args[0] == "ps" && (slices.Contains(args, "--server") || e.Getenv != nil && e.Getenv("ENGINE_SERVER_URL") != "") {
		return true
	}
	return len(args) > 1 && slices.Contains([]string{"run", "tell", "start", "attach"}, args[0]) && control.Number(args[1]) > 0 && (args[1][0] == 'p' || args[1][0] == 't')
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
	ce := controlEnv(e)
	if run.dir != "" {
		path := filepath.Join(run.dir, "server-session.json")
		b, readErr := os.ReadFile(path)
		if readErr == nil {
			var session struct{ URL, Attempt string }
			if err = json.Unmarshal(b, &session); err != nil {
				fmt.Fprintln(e.Err, "engine merge guard: invalid server session")
				return 2
			}
			base := e.Getenv
			ce.Getenv = func(k string) string {
				switch k {
				case "ENGINE_SERVER_URL":
					return session.URL
				case "ENGINE_SERVER_TOKEN_FILE":
					return filepath.Join(run.dir, "server-token")
				case "ENGINE_ATTEMPT_ID":
					return session.Attempt
				case "ENGINE_RUN_DIR":
					return run.dir
				default:
					return base(k)
				}
			}
		} else if !os.IsNotExist(readErr) {
			fmt.Fprintln(e.Err, "engine merge guard:", readErr)
			return 2
		}
	}
	if ce.Getenv("ENGINE_SERVER_URL") == "" {
		return 0
	}
	if err = controlcmd.CheckMerge(ce); err != nil {
		fmt.Fprintln(e.Err, "engine merge guard:", err)
		return 2
	}
	return 0
}
