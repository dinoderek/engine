package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/hook"
	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/session"
)

// hookHome is a home directory holding the runs of three worktrees: a
// hands-off Codex run, a gated Codex run and a hands-off Claude run.
type hookHome struct {
	home                          string
	codex, gated, claude          string // worktrees
	codexRun, gatedRun, claudeRun string // run directories
}

func newHookHome(t *testing.T) hookHome {
	t.Helper()
	h := hookHome{home: t.TempDir()}
	add := func(id, harness, mode string) (worktree, runDir string) {
		worktree = filepath.Join(h.home, "trees", id)
		if err := os.MkdirAll(filepath.Join(worktree, "internal"), 0o755); err != nil {
			t.Fatal(err)
		}
		runDir = filepath.Join(h.home, filepath.FromSlash(session.RunsDir), id+"-20261026T000000Z")
		rec := session.Record{Schema: session.RecordSchema, RunID: id, Harness: harness, Mode: mode, Worktree: worktree, Spawned: stateAt}
		if err := session.WriteRecord(runDir, rec); err != nil {
			t.Fatal(err)
		}
		return worktree, runDir
	}
	h.codex, h.codexRun = add("codex", "codex", "hands-off")
	h.gated, h.gatedRun = add("gated", "codex", "gated")
	h.claude, h.claudeRun = add("claude", "claude", "hands-off")
	return h
}

func (h hookHome) run(args []string, event string, vars map[string]string) (code int, out, errOut string) {
	var o, e bytes.Buffer
	env := Env{In: strings.NewReader(event), Out: &o, Err: &e, Home: h.home, Getenv: func(k string) string { return vars[k] },
		Now: func() time.Time { return stateAt }}
	return Run(args, env), o.String(), e.String()
}

func eventAt(name, cwd string) string {
	return `{"session_id":"` + codexSession + `","hook_event_name":"` + name + `","cwd":"` + cwd + `","tool_name":"Read","tool_input":{"file_path":"/opt/x"}}`
}

func TestStateHookFindsItsRunByCwd(t *testing.T) {
	tests := []struct {
		name, event string
		cwd         func(hookHome) string
		runDir      func(hookHome) string // the run that must hold the state; nil for none
		want        string
	}{
		{"the worktree", "Stop", func(h hookHome) string { return h.codex }, func(h hookHome) string { return h.codexRun }, session.StateWaiting},
		{"a folder below it", "UserPromptSubmit", func(h hookHome) string { return filepath.Join(h.codex, "internal") }, func(h hookHome) string { return h.codexRun }, session.StateWorking},
		{"another run's worktree", "SessionEnd", func(h hookHome) string { return h.gated }, func(h hookHome) string { return h.gatedRun }, session.StateEnded},
		{"an attended request waits", "PermissionRequest", func(h hookHome) string { return h.gated }, func(h hookHome) string { return h.gatedRun }, session.StateWaiting},
		{"an unattended request leaves the state alone", "PermissionRequest", func(h hookHome) string { return h.codex }, nil, ""},
		{"a folder in no worktree", "Stop", func(h hookHome) string { return h.home }, nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHookHome(t)
			code, _, stderr := h.run([]string{"hook", "state"}, eventAt(tt.event, tt.cwd(h)), nil)
			if code != 0 {
				t.Fatalf("exit %d, stderr %q", code, stderr)
			}
			for _, dir := range []string{h.codexRun, h.gatedRun, h.claudeRun} {
				got, ok := session.ReadHarnessState(dir)
				if tt.runDir != nil && dir == tt.runDir(h) {
					want := session.HarnessState{State: tt.want, Event: tt.event, At: stateAt, SessionID: codexSession}
					if !ok || got != want {
						t.Errorf("%s: state %+v (read %v), want %+v", filepath.Base(dir), got, ok, want)
					}
				} else if ok {
					t.Errorf("%s: a state file was written: %+v", filepath.Base(dir), got)
				}
			}
		})
	}
}

func TestStateHookPrefersTheEnvironmentRun(t *testing.T) {
	h := newHookHome(t)
	dir := t.TempDir()
	code, _, stderr := h.run([]string{"hook", "state"}, eventAt("Stop", h.codex), map[string]string{session.RunDirVar: dir})
	if _, ok := session.ReadHarnessState(dir); code != 0 || !ok {
		t.Errorf("exit %d (%q), state in the exported run directory %v", code, stderr, ok)
	}
	if _, ok := session.ReadHarnessState(h.codexRun); ok {
		t.Error("the run found by cwd was written too")
	}
}

func TestPermissionHookFindsItsRunByCwd(t *testing.T) {
	tests := []struct {
		name   string
		cwd    func(hookHome) string
		vars   map[string]string
		denied bool
		run    func(hookHome) string // the run whose log must hold the request; nil for none
		logged string
	}{
		{"a hands-off Codex run is left to auto-review", func(h hookHome) string { return h.codex }, nil, false, func(h hookHome) string { return h.codexRun }, hook.AutoReview},
		{"from a folder below the worktree", func(h hookHome) string { return filepath.Join(h.codex, "internal") }, nil, false, func(h hookHome) string { return h.codexRun }, hook.AutoReview},
		{"a Claude session by hand in a run's worktree prompts as usual", func(h hookHome) string { return h.claude }, nil, false, nil, ""},
		{"a gated run prompts as usual", func(h hookHome) string { return h.gated }, nil, false, nil, ""},
		{"elsewhere nothing happens", func(h hookHome) string { return h.home }, nil, false, nil, ""},
		{"elsewhere, the variables still deny", func(h hookHome) string { return h.home }, map[string]string{session.UnattendedVar: "1"}, true, nil, ""},
		{"the variables win", func(h hookHome) string { return h.codex }, map[string]string{session.UnattendedVar: "1", session.HarnessVar: "claude"}, true, func(h hookHome) string { return h.codexRun }, `"decision":"deny"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHookHome(t)
			code, out, stderr := h.run([]string{"hook", "permission"}, eventAt("PermissionRequest", tt.cwd(h)), tt.vars)
			if code != 0 {
				t.Fatalf("exit %d, stderr %q", code, stderr)
			}
			if got := strings.Contains(out, `"deny"`); got != tt.denied {
				t.Errorf("denied %v, want %v: %q", got, tt.denied, out)
			}
			for _, dir := range []string{h.codexRun, h.gatedRun, h.claudeRun} {
				log, _ := os.ReadFile(filepath.Join(dir, session.PermissionLog))
				if tt.run != nil && dir == tt.run(h) {
					if !strings.Contains(string(log), tt.logged) {
						t.Errorf("%s: log %q lacks %q", filepath.Base(dir), log, tt.logged)
					}
				} else if len(log) != 0 {
					t.Errorf("%s: logged %q", filepath.Base(dir), log)
				}
			}
		})
	}
}

func TestStateHookStartsTheLabelForAJoinedThread(t *testing.T) {
	tests := []struct {
		name, event string
		vars        bool   // the run comes from the environment: an embedded session
		record      string // codex-label.json already in the run, or ""
		detached    bool
	}{
		{"a joined session's prompt", "UserPromptSubmit", false, "", true},
		{"a joined session's tool call", "PostToolUse", false, "", true},
		{"a joined session's stop", "Stop", false, "", true},
		{"a joined session's start", "SessionStart", false, "", false},
		{"an embedded session's prompt", "UserPromptSubmit", true, "", false},
		{"a labelled thread", "UserPromptSubmit", false, `{"attempts":1}`, false},
		{"a failure with attempts left", "Stop", false, `{"attempts":1,"error":"x"}`, true},
		{"no attempts left", "Stop", false, `{"attempts":3,"error":"x"}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHookHome(t)
			if tt.record != "" {
				if err := os.WriteFile(filepath.Join(h.codexRun, hostops.CodexLabelFile), []byte(tt.record), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			var vars map[string]string
			if tt.vars {
				vars = map[string]string{session.RunDirVar: h.codexRun, session.HarnessVar: "codex"}
			}
			var started [][]string
			var e bytes.Buffer
			env := Env{In: strings.NewReader(eventAt(tt.event, h.codex)), Out: &bytes.Buffer{}, Err: &e, Home: h.home,
				Getenv: func(k string) string { return vars[k] }, Now: func() time.Time { return stateAt },
				Detach: func(args ...string) error { started = append(started, args); return nil }}
			if code := Run([]string{"hook", "state"}, env); code != 0 {
				t.Fatalf("exit %d, stderr %q", code, e.String())
			}
			var want [][]string
			if tt.detached {
				want = [][]string{{"hook", "codex-label", h.codexRun, codexSession}}
			}
			if !reflect.DeepEqual(started, want) {
				t.Errorf("started %v, want %v", started, want)
			}
		})
	}
}

func TestStateHookReportsALabelItCannotStart(t *testing.T) {
	for name, detach := range map[string]func(...string) error{
		"no detach":    nil,
		"detach fails": func(...string) error { return errors.New("fork failed") },
	} {
		t.Run(name, func(t *testing.T) {
			h := newHookHome(t)
			var e bytes.Buffer
			env := Env{In: strings.NewReader(eventAt("UserPromptSubmit", h.codex)), Out: &bytes.Buffer{}, Err: &e, Home: h.home,
				Getenv: func(string) string { return "" }, Now: func() time.Time { return stateAt }, Detach: detach}
			if code := Run([]string{"hook", "state"}, env); code != 0 || !strings.Contains(e.String(), "label the Codex thread") {
				t.Errorf("exit %d, stderr %q", code, e.String())
			}
			if st, ok := session.ReadHarnessState(h.codexRun); !ok || st.State != session.StateWorking {
				t.Errorf("state %+v, %v", st, ok)
			}
		})
	}
}

func TestCodexLabelHookRecordsItsAttempt(t *testing.T) {
	h := newHookHome(t)
	// The run names no repo, so the attempt fails and is recorded.
	code, _, stderr := h.run([]string{"hook", "codex-label", h.codexRun, codexSession}, "", nil)
	if code != 1 || !strings.Contains(stderr, "engine hook codex-label:") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
	if data, err := os.ReadFile(filepath.Join(h.codexRun, hostops.CodexLabelFile)); err != nil || !strings.Contains(string(data), `"attempts": 1`) {
		t.Errorf("record %s, %v", data, err)
	}
	if code, _, _ := h.run([]string{"hook", "codex-label", filepath.Join(h.home, "missing"), codexSession}, "", nil); code != 1 {
		t.Errorf("a run with no run.json: exit %d", code)
	}
}
