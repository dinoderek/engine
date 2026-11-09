package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/harness/harnesstest"
	"github.com/dinoderek/engine/internal/hook"
	"github.com/dinoderek/engine/internal/session"
)

// fakeHookEnv runs hooks against the one adapter f, in a home holding a
// hands-off run of f's harness whose worktree is the returned folder.
func fakeHookEnv(t *testing.T, f *harnesstest.Fake, in string, vars map[string]string) (Env, *bytes.Buffer, *bytes.Buffer, string, string) {
	t.Helper()
	home := t.TempDir()
	worktree := filepath.Join(home, "trees", "w")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	runDir := filepath.Join(home, filepath.FromSlash(session.RunsDir), "w-20261026T000000Z")
	rec := session.Record{Schema: session.RecordSchema, RunID: "w", Repo: "o/w", Harness: f.Called, Mode: "hands-off", Worktree: worktree, Spawned: stateAt}
	if err := session.WriteRecord(runDir, rec); err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	env := Env{In: strings.NewReader(strings.ReplaceAll(in, "@worktree", worktree)), Out: &out, Err: &errs, Home: home,
		Getenv: func(k string) string { return vars[k] }, Now: func() time.Time { return stateAt }, Harnesses: harness.NewAdapters(f)}
	return env, &out, &errs, worktree, runDir
}

// The core hooks ask the named adapter, whatever it is: its event shape,
// whether its hooks find their run, its policy and denial, and what it does
// once a state is recorded.
func TestCoreHooksRunOnTheNamedAdapter(t *testing.T) {
	f := &harnesstest.Fake{Called: "fake", Finds: true, Detach: harness.TurnStopped}
	var started [][]string
	env, _, errs, _, runDir := fakeHookEnv(t, f, `{"kind":"turn-stopped","name":"Halt","session_id":"s1","cwd":"@worktree"}`, nil)
	env.Detach = func(args ...string) error { started = append(started, args); return nil }
	if code := Run([]string{"hook", "state", "--harness", "fake"}, env); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errs.String())
	}
	if st, ok := session.ReadHarnessState(runDir); !ok || st.State != session.StateWaiting || st.Event != "Halt" || st.SessionID != "s1" {
		t.Errorf("state %+v, %v", st, ok)
	}
	events, runs := f.Signalled()
	if want := (harness.HookRun{Dir: runDir, Repo: "o/w", Joined: true, Unattended: true}); len(events) != 1 || events[0].Name != "Halt" || !reflect.DeepEqual(runs, []harness.HookRun{want}) {
		t.Errorf("signalled %+v for %+v", events, runs)
	}
	if want := [][]string{{"hook", "fake-entry", "--harness", "fake", runDir, "s1"}}; !reflect.DeepEqual(started, want) {
		t.Errorf("started %v, want %v", started, want)
	}

	// The same run, found the same way, gets the adapter's denial.
	env, out, errs, _, runDir := fakeHookEnv(t, f, `{"kind":"permission-requested","name":"Ask","cwd":"@worktree"}`, nil)
	var denied struct{ Denied string }
	if code := Run([]string{"hook", "permission", "--harness", "fake"}, env); code != 0 || json.Unmarshal(out.Bytes(), &denied) != nil || denied.Denied != hook.DenyMessage {
		t.Errorf("exit %d, out %q, stderr %q", code, out.String(), errs.String())
	}
	if log, _ := os.ReadFile(filepath.Join(runDir, session.PermissionLog)); !strings.Contains(string(log), `"event":"Ask","tool":"","decision":"deny"`) {
		t.Errorf("log %q", log)
	}
}

// An adapter whose hooks do not find their run never takes one by cwd, and
// one whose policy leaves requests to review writes no denial.
func TestCoreHooksKeepToTheAdaptersAnswers(t *testing.T) {
	f := &harnesstest.Fake{Called: "fake", Policy: harness.LeaveToReview}
	env, out, errs, _, runDir := fakeHookEnv(t, f, `{"kind":"permission-requested","name":"Ask","cwd":"@worktree"}`,
		map[string]string{session.UnattendedVar: "1"})
	if code := Run([]string{"hook", "permission", "--harness", "fake"}, env); code != 0 || out.Len() != 0 {
		t.Errorf("exit %d, out %q, stderr %q", code, out.String(), errs.String())
	}
	if _, err := os.Stat(filepath.Join(runDir, session.PermissionLog)); err == nil {
		t.Error("a run found by cwd was logged to")
	}
	env, _, _, _, runDir = fakeHookEnv(t, f, `{"kind":"turn-stopped","cwd":"@worktree"}`, nil)
	if code := Run([]string{"hook", "state", "--harness", "fake"}, env); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if _, ok := session.ReadHarnessState(runDir); ok {
		t.Error("a run found by cwd was recorded")
	}
	if events, _ := f.Signalled(); len(events) != 0 {
		t.Errorf("signalled outside a run: %+v", events)
	}
}

// An adapter's own entry gets the arguments after the harness and the run
// directory the launch exported; its error fails open, named for the entry.
func TestAdapterEntriesGetTheirArguments(t *testing.T) {
	var got harness.Call
	f := &harnesstest.Fake{Called: "fake", Own: map[string]harness.Entry{
		"ping": func(c harness.Call) error { got = c; _, err := c.Out.Write([]byte("pong\n")); return err },
		"fail": func(harness.Call) error { return errors.New("broken") },
	}}
	env, out, errs, _, _ := fakeHookEnv(t, f, "", map[string]string{session.RunDirVar: "/runs/r"})
	if code := Run([]string{"hook", "ping", "--harness", "fake", "a", "b"}, env); code != 0 || out.String() != "pong\n" {
		t.Errorf("exit %d, out %q, stderr %q", code, out.String(), errs.String())
	}
	if !reflect.DeepEqual(got.Args, []string{"a", "b"}) || got.RunDir != "/runs/r" {
		t.Errorf("call %+v", got)
	}
	if code := Run([]string{"hook", "fail", "--harness", "fake"}, env); code != 1 || !strings.Contains(errs.String(), "engine hook fail: broken") {
		t.Errorf("exit %d, stderr %q", code, errs.String())
	}
	errs.Reset()
	if code := Run([]string{"hook", "label", "--harness", "fake"}, env); code != 1 || !strings.Contains(errs.String(), "(want pre-tool-use, permission, state, fail, ping)") {
		t.Errorf("exit %d, stderr %q", code, errs.String())
	}
}
