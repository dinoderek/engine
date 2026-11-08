package cli

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/dinoderek/engine/internal/hook"
	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/session"
)

// labelEvents are the events that start an attempt to label a joined Codex
// session's thread: the first prompt, then each tool call and stop until one
// succeeds or the attempts run out, since the first may come before the
// daemon has stored the thread.
var labelEvents = map[string]bool{"UserPromptSubmit": true, "PostToolUse": true, "Stop": true}

// labelCodexThread starts `engine hook codex-label` in the background for a
// joined Codex session whose thread is not yet labelled. The hook runs inside
// the daemon it would call, so it never waits for the daemon itself. A failure
// to start is reported, and the session carries on.
func labelCodexThread(env Env, run hookRun, sig hook.Signal) {
	if !run.joined || !labelEvents[sig.Event] || sig.SessionID == "" || !hostops.CodexLabelPending(run.dir) {
		return
	}
	detach := env.Detach
	if detach == nil {
		fmt.Fprintln(env.Err, "engine hook: label the Codex thread: no way to start it in the background")
		return
	}
	if err := detach("hook", "codex-label", run.dir, sig.SessionID); err != nil {
		fmt.Fprintln(env.Err, "engine hook: label the Codex thread:", err)
	}
}

// codexLabelHook labels the thread of the run in runDir, recording the outcome
// there.
func codexLabelHook(env Env, runDir, threadID string) int {
	rec, err := session.ReadRecord(runDir)
	if err == nil {
		err = hostops.LabelCodexThread(hostDeps(env), runDir, rec, threadID)
	}
	if err != nil {
		fmt.Fprintln(env.Err, "engine hook codex-label:", err)
		return 1
	}
	return 0
}

// detachSelf starts this binary with args in its own session, with no
// standard streams, and does not wait for it.
func detachSelf(args ...string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(self, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
