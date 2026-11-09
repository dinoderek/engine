package hook

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/harness/claude"
	"github.com/dinoderek/engine/internal/harness/codex"
)

// codexEvent reads an event recorded from a live Codex session, as the Codex
// adapter reads it. The paths in it are scrubbed; the fields are as Codex
// sent them.
func codexEvent(t *testing.T, name string) harness.Event {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "harness", "codex", "testdata", "hooks", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return parse(t, codexHooks, string(data))
}

var (
	codexHooks  = codex.New(harness.Env{}, codex.Options{}).Hooks()
	claudeHooks = claude.New(harness.Env{}).Hooks()
)

// parse reads raw as hooks read it.
func parse(t *testing.T, hooks harness.Hooks, raw string) harness.Event {
	t.Helper()
	e, err := hooks.Event([]byte(raw))
	if err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return e
}

// codexSession is the session ID in every recorded event.
const codexSession = "01a1752f-8cf9-7592-bdbd-7a42730e503a"

func TestStateFromRecordedEvents(t *testing.T) {
	tests := []struct {
		event      string
		unattended bool
		want       string // "" means nothing is written
	}{
		{"SessionStart", false, "waiting"},
		{"UserPromptSubmit", false, "working"},
		{"PostToolUse", false, "working"},
		{"PostToolUse", true, "working"},
		{"Stop", false, "waiting"},
		{"Interrupt", false, "waiting"},
		{"PermissionRequest", false, "waiting"},
		{"SessionEnd", false, "ended"},
		{"SessionStart", true, "waiting"},
		{"PermissionRequest", true, ""},
		{"PreToolUse", false, "working"},
	}
	for _, tt := range tests {
		name := tt.event
		if tt.unattended {
			name += " unattended"
		}
		t.Run(name, func(t *testing.T) {
			var got []Signal
			var errs bytes.Buffer
			s := State{Err: &errs, Unattended: tt.unattended, Write: func(sig Signal) error { got = append(got, sig); return nil }}
			if code := s.Handle(codexEvent(t, tt.event)); code != 0 {
				t.Fatalf("exit %d, stderr %q", code, errs.String())
			}
			if tt.want == "" {
				if len(got) != 0 {
					t.Errorf("wrote %+v, want nothing", got)
				}
				return
			}
			want := Signal{State: tt.want, Event: tt.event, SessionID: codexSession}
			if tt.event != "SessionStart" && tt.event != "SessionEnd" {
				want.TurnID = "01a17530-2040-7420-9c65-9723141e59e1"
			}
			if len(got) != 1 || got[0] != want {
				t.Errorf("wrote %+v, want %+v", got, want)
			}
		})
	}
}

func TestStateFailsOpen(t *testing.T) {
	var errs bytes.Buffer
	code := State{Err: &errs, Write: func(Signal) error { return errors.New("disk full") }}.Handle(codexEvent(t, "Stop"))
	if code != 1 || !strings.Contains(errs.String(), "disk full") {
		t.Errorf("exit %d, stderr %q; want 1, %q (never 2, which blocks)", code, errs.String(), "disk full")
	}
}

// withCommand returns the recorded PreToolUse event with another shell
// command in it.
func withCommand(t *testing.T, command string) harness.Event {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "harness", "codex", "testdata", "hooks", "PreToolUse.json"))
	if err != nil {
		t.Fatal(err)
	}
	var e map[string]any
	if err := json.Unmarshal(data, &e); err != nil {
		t.Fatal(err)
	}
	e["tool_input"] = map[string]string{"command": command}
	if data, err = json.Marshal(e); err != nil {
		t.Fatal(err)
	}
	return parse(t, codexHooks, string(data))
}

func TestMergeGuardReadsCodexShellEvents(t *testing.T) {
	tests := map[string]struct {
		in     harness.Event
		code   int
		stderr string
	}{
		"recorded event":  {codexEvent(t, "PreToolUse"), 0, ""},
		"unpinned merge":  {withCommand(t, "gh pr merge 7 --squash"), 2, "needs --match-head-commit"},
		"auto merge":      {withCommand(t, "gh pr merge 7 --auto --match-head-commit abc"), 2, "auto-merge is not allowed"},
		"pinned merge":    {withCommand(t, "gh pr merge 7 --squash --match-head-commit abc"), 0, ""},
		"zsh login shell": {withCommand(t, "/bin/zsh -lc 'gh pr merge 7'"), 2, "needs --match-head-commit"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var errs bytes.Buffer
			if code := PreToolUse(tt.in, &errs); code != tt.code || !strings.Contains(errs.String(), tt.stderr) {
				t.Errorf("exit %d, stderr %q; want %d, %q", code, errs.String(), tt.code, tt.stderr)
			}
		})
	}
}

// An unattended Codex session's request is only logged: Codex asks the hook
// before auto-review, and a deny would pre-empt the review.
func TestPermissionLeavesAnUnattendedCodexRequestToAutoReview(t *testing.T) {
	var out, errs, log bytes.Buffer
	p := Permission{Out: &out, Err: &errs, Unattended: true, Policy: codexHooks.Unattended(), Deny: codexHooks.Deny, Log: &log, Now: at}
	if code := p.Handle(codexEvent(t, "PermissionRequest")); code != 0 || errs.Len() != 0 || out.Len() != 0 {
		t.Fatalf("exit %d, out %q, stderr %q", code, out.String(), errs.String())
	}
	var entry Entry
	if err := json.Unmarshal(log.Bytes(), &entry); err != nil {
		t.Fatalf("log %q: %v", log.String(), err)
	}
	if entry.Tool != "Bash" || entry.Decision != harness.LeaveToReview.Decision() || !strings.Contains(string(entry.Input), "touch $HOME/codex-cap-probe.txt") {
		t.Errorf("entry %+v", entry)
	}
}

// Under the deny policy, Codex's request is denied in the shape Codex reads.
func TestPermissionHandlesCodexRequests(t *testing.T) {
	var out, errs, log bytes.Buffer
	p := Permission{Out: &out, Err: &errs, Unattended: true, Policy: harness.Deny, Deny: codexHooks.Deny, Log: &log, Now: at}
	if code := p.Handle(codexEvent(t, "PermissionRequest")); code != 0 || errs.Len() != 0 {
		t.Fatalf("exit %d, stderr %q", code, errs.String())
	}
	var got struct {
		Output struct {
			Event    string `json:"hookEventName"`
			Decision struct {
				Behavior string `json:"behavior"`
				Message  string `json:"message"`
			} `json:"decision"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decision %q: %v", out.String(), err)
	}
	if got.Output.Event != "PermissionRequest" || got.Output.Decision.Behavior != "deny" || got.Output.Decision.Message != DenyMessage {
		t.Errorf("decision %+v", got)
	}
	var entry Entry
	if err := json.Unmarshal(log.Bytes(), &entry); err != nil {
		t.Fatalf("log %q: %v", log.String(), err)
	}
	if entry.Tool != "Bash" || entry.Decision != "deny" || entry.Mode != "default" || !strings.Contains(string(entry.Input), "touch $HOME/codex-cap-probe.txt") {
		t.Errorf("entry %+v", entry)
	}
}

func TestPermissionLeavesAnAttendedCodexRequestAlone(t *testing.T) {
	var out, errs, log bytes.Buffer
	p := Permission{Out: &out, Err: &errs, Log: &log, Policy: codexHooks.Unattended(), Deny: codexHooks.Deny, Now: at}
	if code := p.Handle(codexEvent(t, "PermissionRequest")); code != 0 || out.Len() != 0 || log.Len() != 0 || errs.Len() != 0 {
		t.Errorf("exit %d, out %q, log %q, stderr %q", code, out.String(), log.String(), errs.String())
	}
}

// Claude Code's hook events, in the shape it sends them, set the state the
// host reads to tell a turn that ended from one under way.
func TestStateFromClaudeEvents(t *testing.T) {
	const id = "b0846859-b053-4a13-bab8-aeff8f1fd096"
	event := func(name, extra string) string {
		return `{"session_id":"` + id + `","transcript_path":"/home/u/.claude/projects/p/` + id + `.jsonl","cwd":"/w","permission_mode":"auto","hook_event_name":"` + name + `"` + extra + `}`
	}
	for _, tt := range []struct{ name, in, want string }{
		{"UserPromptSubmit", event("UserPromptSubmit", `,"prompt":"Read the new task updates"`), "working"},
		{"PreToolUse", event("PreToolUse", `,"tool_name":"Bash","tool_input":{"command":"./dev test"},"tool_use_id":"toolu_1"`), "working"},
		{"PostToolUse", event("PostToolUse", `,"tool_name":"Bash","tool_input":{"command":"./dev test","run_in_background":true},"tool_response":{"backgroundTaskId":"b1"}`), "working"},
		{"Stop", event("Stop", `,"stop_hook_active":false`), "waiting"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var got []Signal
			s := State{Err: &bytes.Buffer{}, Unattended: true, Write: func(sig Signal) error { got = append(got, sig); return nil }}
			if code := s.Handle(parse(t, claudeHooks, tt.in)); code != 0 {
				t.Fatalf("exit %d", code)
			}
			if want := []Signal{{State: tt.want, Event: tt.name, SessionID: id}}; !reflect.DeepEqual(got, want) {
				t.Errorf("wrote %+v, want %+v", got, want)
			}
		})
	}
}
