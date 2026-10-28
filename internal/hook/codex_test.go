package hook

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// codexEvent reads an event recorded from a live Codex session. The paths in
// it are scrubbed; the fields are as Codex sent them.
func codexEvent(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "codex", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
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
		{"PreToolUse", false, ""},
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
			if code := s.Handle(strings.NewReader(codexEvent(t, tt.event))); code != 0 {
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
	tests := map[string]struct {
		in     string
		write  func(Signal) error
		stderr string
	}{
		"unreadable event": {"not json", nil, "unreadable state event"},
		"write fails":      {codexEvent(t, "Stop"), func(Signal) error { return errors.New("disk full") }, "disk full"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var errs bytes.Buffer
			code := State{Err: &errs, Write: tt.write}.Handle(strings.NewReader(tt.in))
			if code != 1 || !strings.Contains(errs.String(), tt.stderr) {
				t.Errorf("exit %d, stderr %q; want 1, %q (never 2, which blocks)", code, errs.String(), tt.stderr)
			}
		})
	}
}

// withCommand returns the recorded PreToolUse event with another shell
// command in it.
func withCommand(t *testing.T, command string) string {
	t.Helper()
	var e map[string]any
	if err := json.Unmarshal([]byte(codexEvent(t, "PreToolUse")), &e); err != nil {
		t.Fatal(err)
	}
	e["tool_input"] = map[string]string{"command": command}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestMergeGuardReadsCodexShellEvents(t *testing.T) {
	tests := map[string]struct {
		in     string
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
			if code := PreToolUse(strings.NewReader(tt.in), &errs); code != tt.code || !strings.Contains(errs.String(), tt.stderr) {
				t.Errorf("exit %d, stderr %q; want %d, %q", code, errs.String(), tt.code, tt.stderr)
			}
		})
	}
}

// An unattended Codex session's request is only logged: Codex asks the hook
// before auto-review, and a deny would pre-empt the review.
func TestPermissionLeavesAnUnattendedCodexRequestToAutoReview(t *testing.T) {
	var out, errs, log bytes.Buffer
	p := Permission{Out: &out, Err: &errs, Unattended: true, Codex: true, Log: &log, Now: at}
	if code := p.Handle(strings.NewReader(codexEvent(t, "PermissionRequest"))); code != 0 || errs.Len() != 0 || out.Len() != 0 {
		t.Fatalf("exit %d, out %q, stderr %q", code, out.String(), errs.String())
	}
	var entry Entry
	if err := json.Unmarshal(log.Bytes(), &entry); err != nil {
		t.Fatalf("log %q: %v", log.String(), err)
	}
	if entry.Tool != "Bash" || entry.Decision != AutoReview || !strings.Contains(string(entry.Input), "touch $HOME/codex-cap-probe.txt") {
		t.Errorf("entry %+v", entry)
	}
}

// The hook reads Codex's request as it reads Claude's: outside a Codex
// session engine launched, an unattended request is denied.
func TestPermissionHandlesCodexRequests(t *testing.T) {
	var out, errs, log bytes.Buffer
	p := Permission{Out: &out, Err: &errs, Unattended: true, Log: &log, Now: at}
	if code := p.Handle(strings.NewReader(codexEvent(t, "PermissionRequest"))); code != 0 || errs.Len() != 0 {
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
	p := Permission{Out: &out, Err: &errs, Log: &log, Now: at}
	if code := p.Handle(strings.NewReader(codexEvent(t, "PermissionRequest"))); code != 0 || out.Len() != 0 || log.Len() != 0 || errs.Len() != 0 {
		t.Errorf("exit %d, out %q, log %q, stderr %q", code, out.String(), log.String(), errs.String())
	}
}
