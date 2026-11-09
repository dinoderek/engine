package claude

import (
	"testing"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/harness/harnesstest"
)

// claudeSession is the session ID in every Claude Code event below.
const claudeSession = "b0846859-b053-4a13-bab8-aeff8f1fd096"

// claudeEvent is a hook event in the shape Claude Code sends it.
func claudeEvent(name, extra string) string {
	return `{"session_id":"` + claudeSession + `","transcript_path":"/home/u/.claude/projects/p/` + claudeSession +
		`.jsonl","cwd":"/w","permission_mode":"auto","hook_event_name":"` + name + `"` + extra + `}`
}

// events are Claude Code's hook events engine reads, with their kinds.
var events = []harnesstest.Event{
	{Raw: claudeEvent("SessionStart", `,"source":"startup"`), Name: "SessionStart", Kind: harness.SessionStarted},
	{Raw: claudeEvent("UserPromptSubmit", `,"prompt":"Read the new task updates"`), Name: "UserPromptSubmit", Kind: harness.PromptSubmitted},
	{Raw: claudeEvent("PreToolUse", `,"tool_name":"Bash","tool_input":{"command":"./dev test"},"tool_use_id":"toolu_1"`), Name: "PreToolUse", Kind: harness.ToolStarting},
	{Raw: claudeEvent("PostToolUse", `,"tool_name":"Bash","tool_input":{"command":"./dev test","run_in_background":true},"tool_response":{"backgroundTaskId":"b1"}`), Name: "PostToolUse", Kind: harness.ToolFinished},
	{Raw: claudeEvent("PermissionRequest", `,"tool_name":"Read","tool_input":{"file_path":"/opt/x"}`), Name: "PermissionRequest", Kind: harness.PermissionRequested},
	{Raw: claudeEvent("PermissionDenied", `,"tool_name":"Bash","tool_input":{"command":"rm -rf /x"},"reason":"[Irreversible Local Destruction]"`), Name: "PermissionDenied", Kind: harness.PermissionDenied},
	{Raw: claudeEvent("Stop", `,"stop_hook_active":false`), Name: "Stop", Kind: harness.TurnStopped},
	{Raw: claudeEvent("SessionEnd", `,"reason":"exit"`), Name: "SessionEnd", Kind: harness.SessionEnded},
	{Raw: claudeEvent("StopFailure", `,"error":"rate_limit"`), Name: "StopFailure", Kind: harness.Other},
}

func TestContract(t *testing.T) {
	harnesstest.Contract(t, func(env harness.Env) harness.Adapter { return New(env) }, harnesstest.Fixtures{Events: events})
}
