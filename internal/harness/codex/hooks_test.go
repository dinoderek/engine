package codex

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/harness"
)

// codexSession and codexTurn are the IDs in every recorded event.
const (
	codexSession = "01a1752f-8cf9-7592-bdbd-7a42730e503a"
	codexTurn    = "01a17530-2040-7420-9c65-9723141e59e1"
)

func TestHooksReadRecordedEvents(t *testing.T) {
	h := New(harness.Env{}, Options{}).Hooks()
	for name := range recordedKinds {
		ev, err := h.Event([]byte(recorded(t, name)))
		if err != nil {
			t.Fatal(err)
		}
		turn := codexTurn
		if name == "SessionStart" || name == "SessionEnd" {
			turn = ""
		}
		if ev.SessionID != codexSession || ev.TurnID != turn || ev.Cwd == "" {
			t.Errorf("%s: %+v", name, ev)
		}
	}
	req, _ := h.Event([]byte(recorded(t, "PermissionRequest")))
	if req.Tool != "Bash" || req.Mode != "default" || !strings.Contains(req.Command, "touch $HOME/codex-cap-probe.txt") ||
		!strings.Contains(string(req.Input), "codex-cap-probe.txt") {
		t.Errorf("a permission request: %+v", req)
	}
	if pre, _ := h.Event([]byte(recorded(t, "PreToolUse"))); pre.Tool != "Bash" || pre.Command == "" {
		t.Errorf("a shell call: %+v", pre)
	}
}

// Codex asks the hook before auto-review, so an unattended request is left
// to it; a joined session's hooks find their run by cwd.
func TestHooksLeaveToReviewAndFindTheirRun(t *testing.T) {
	h := New(harness.Env{}, Options{}).Hooks()
	if h.Unattended() != harness.LeaveToReview || !h.FindsRun() {
		t.Errorf("policy %v, finds run %v", h.Unattended(), h.FindsRun())
	}
	var out bytes.Buffer
	if err := h.Deny(&out, "no"); err != nil || !strings.Contains(out.String(), `"behavior":"deny","message":"no"`) {
		t.Errorf("deny wrote %q, %v", out.String(), err)
	}
}

func TestSignalledStartsTheLabelOfAJoinedThread(t *testing.T) {
	joined := harness.HookRun{Joined: true, Repo: "o/engine"}
	tests := []struct {
		name   string
		run    harness.HookRun
		kind   harness.EventKind
		record string // codex-label.json already in the run, or ""
		want   bool
	}{
		{"a prompt", joined, harness.PromptSubmitted, "", true},
		{"a finished tool call", joined, harness.ToolFinished, "", true},
		{"a stop", joined, harness.TurnStopped, "", true},
		{"a session start", joined, harness.SessionStarted, "", false},
		{"an embedded session", harness.HookRun{Repo: "o/engine"}, harness.PromptSubmitted, "", false},
		{"a check run", harness.HookRun{Joined: true}, harness.PromptSubmitted, "", false},
		{"a labelled thread", joined, harness.PromptSubmitted, `{"attempts":1}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.run.Dir = t.TempDir()
			if tt.record != "" {
				write(t, filepath.Join(tt.run.Dir, LabelFile), tt.record, 0o644)
			}
			var started [][]string
			c := harness.HookContext{Run: tt.run, Err: &bytes.Buffer{}, Detach: func(entry string, args ...string) error {
				started = append(started, append([]string{entry}, args...))
				return nil
			}}
			New(harness.Env{Now: fixedNow}, Options{}).Hooks().Signalled(c, harness.Event{Kind: tt.kind, SessionID: "th1"})
			var want [][]string
			if tt.want {
				want = [][]string{{"label", tt.run.Dir, "th1"}}
			}
			if !reflect.DeepEqual(started, want) {
				t.Errorf("started %v, want %v", started, want)
			}
		})
	}
	for name, detach := range map[string]func(string, ...string) error{
		"no detach":    nil,
		"detach fails": func(string, ...string) error { return errors.New("fork failed") },
	} {
		var errs bytes.Buffer
		run := joined
		run.Dir = t.TempDir()
		New(harness.Env{Now: fixedNow}, Options{}).Hooks().Signalled(harness.HookContext{Run: run, Err: &errs, Detach: detach}, harness.Event{Kind: harness.TurnStopped, SessionID: "th1"})
		if !strings.Contains(errs.String(), "label the Codex thread") {
			t.Errorf("%s: stderr %q", name, errs.String())
		}
		if _, err := os.Stat(filepath.Join(run.Dir, LabelFile)); err == nil {
			t.Errorf("%s: an attempt was recorded", name)
		}
	}
}

func TestLabelEntryWantsARunAndAThread(t *testing.T) {
	label := New(harness.Env{Now: fixedNow}, Options{}).Hooks().Entries()["label"]
	if err := label(harness.Call{Args: []string{"/r"}}); err == nil || !strings.Contains(err.Error(), "want <run-dir> <thread>") {
		t.Errorf("one argument: %v", err)
	}
	if err := label(harness.Call{Args: []string{filepath.Join(t.TempDir(), "missing"), "th1"}}); err == nil {
		t.Error("a run with no run.json did not fail")
	}
}
