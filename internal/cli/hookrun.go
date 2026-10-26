package cli

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/session"
)

// hookRun is the run a harness hook acts for: where its files go, the harness
// and whether nobody watches the session.
type hookRun struct {
	dir        string
	harness    string
	unattended bool
}

// findHookRun reads a hook event from env.In and works out its run. With
// ENGINE_RUN_DIR set it takes the run from the environment engine start
// exported. Without it, which is how a Codex session that joined a daemon
// sees things, it finds the run whose worktree holds the event's cwd and reads
// the harness and mode from that run's record, but only a Codex run: a Claude
// session always has its variables, and one opened by hand in a run's
// worktree is not that run's session. Where the cwd is in no such run's
// worktree the environment's own values stand, and the hook does as it always
// did. The returned reader replays the event for the hook.
func findHookRun(env Env) (hookRun, io.Reader) {
	run := hookRun{dir: env.Getenv(session.RunDirVar), harness: env.Getenv(session.HarnessVar), unattended: env.Getenv(session.UnattendedVar) == "1"}
	if run.dir != "" {
		return run, env.In
	}
	event, err := io.ReadAll(env.In)
	if err != nil {
		return run, bytes.NewReader(event)
	}
	var e struct {
		Cwd string `json:"cwd"`
	}
	if json.Unmarshal(event, &e) == nil {
		if dir, rec, ok := session.FindRun(env.Home, e.Cwd); ok && rec.Harness == hostops.Codex {
			run.dir = dir
			if run.harness == "" {
				run.harness = rec.Harness
			}
			run.unattended = run.unattended || rec.Mode == hostops.HandsOff
		}
	}
	return run, bytes.NewReader(event)
}
