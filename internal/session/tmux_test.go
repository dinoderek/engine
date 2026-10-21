package session

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fake is a Runner that records every call as one line and answers by the
// tmux subcommand.
type fake struct {
	calls   []string
	answers map[string]func() (string, error)
}

func (f *fake) run(name string, args ...string) (string, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	if len(args) > 0 && args[0] == "-L" {
		args = args[2:]
	}
	if answer, ok := f.answers[args[0]]; ok {
		return answer()
	}
	return "", nil
}

func (f *fake) answer(sub string, out string, err error) {
	if f.answers == nil {
		f.answers = map[string]func() (string, error){}
	}
	f.answers[sub] = func() (string, error) { return out, err }
}

func (f *fake) dump() string { return strings.Join(f.calls, "\n") }

var noSession = &ExitError{Command: "tmux has-session", Code: 1, Stderr: "no server running"}

func newFake(t *testing.T) (*Tmux, *fake) {
	t.Helper()
	f := &fake{}
	f.answer("has-session", "", noSession)
	f.answer("new-session", "@0 %0\n", nil)
	f.answer("new-window", "@1 %1\n", nil)
	return &Tmux{Run: f.run, Bin: "tmux"}, f
}

func window(t *testing.T) Window {
	t.Helper()
	dir := t.TempDir()
	return Window{Name: "boga3-412", Dir: dir, Command: "claude --name boga3-412", Log: filepath.Join(dir, "runs", "r1", "pane.log")}
}

func TestOpenCreatesTheSessionWithItsFirstWindow(t *testing.T) {
	tm, f := newFake(t)
	w := window(t)
	got, err := tm.Open(w)
	if err != nil {
		t.Fatal(err)
	}
	if want := (WindowInfo{Name: "boga3-412", ID: "@0", PaneID: "%0"}); got.WindowInfo != want || got.AwakeErr != nil {
		t.Errorf("got %+v", got)
	}
	want := []string{
		"tmux has-session -t =engine:",
		"tmux new-session -d -P -F #{window_id} #{pane_id} -s engine -n boga3-412 -c " + w.Dir + " sleep 300",
		"tmux set-option -t =engine: mouse on",
		"tmux set-option -t =engine: history-limit 50000",
		"tmux set-option -w -t @0 allow-rename off",
		"tmux set-option -w -t @0 automatic-rename off",
		"tmux set-option -w -t @0 window-size latest",
		"tmux set-option -w -t @0 remain-on-exit on",
		"tmux pipe-pane -t %0 cat >> '" + w.Log + "'",
		"tmux respawn-pane -k -t %0 -c " + w.Dir + " claude --name boga3-412",
	}
	if !reflect.DeepEqual(f.calls, want) {
		t.Errorf("calls:\n%s\nwant:\n%s", f.dump(), strings.Join(want, "\n"))
	}
	if info, err := os.Stat(filepath.Dir(w.Log)); err != nil || !info.IsDir() {
		t.Errorf("the log's directory was not created: %v", err)
	}
}

func TestOpenAddsAWindowToTheRunningSession(t *testing.T) {
	tm, f := newFake(t)
	f.answer("has-session", "", nil)
	f.answer("list-windows", "@0 %0 0 other-1\n", nil)
	w := window(t)
	w.Log = ""
	got, err := tm.Open(w)
	if err != nil || got.ID != "@1" || got.PaneID != "%1" {
		t.Fatalf("got %+v, %v", got, err)
	}
	d := f.dump()
	if strings.Contains(d, "new-session") || strings.Contains(d, "pipe-pane") {
		t.Errorf("unexpected calls:\n%s", d)
	}
	if !strings.Contains(d, "tmux new-window -d -P -F #{window_id} #{pane_id} -t =engine: -n boga3-412 -c "+w.Dir+" sleep 300") {
		t.Errorf("no new-window:\n%s", d)
	}
	if !strings.Contains(d, "set-option -t =engine: mouse on") {
		t.Errorf("session options are applied every time:\n%s", d)
	}
}

func TestOpenRefusesAnExistingName(t *testing.T) {
	tm, f := newFake(t)
	f.answer("has-session", "", nil)
	f.answer("list-windows", "@0 %0 0 boga3-412\n", nil)
	_, err := tm.Open(window(t))
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("got %v", err)
	}
	if strings.Contains(f.dump(), "new-window") {
		t.Error("it created the window anyway")
	}
}

func TestOpenSurvivesACreateRace(t *testing.T) {
	tm, f := newFake(t)
	answers := []error{noSession, nil}
	f.answers["has-session"] = func() (string, error) {
		err := answers[0]
		answers = answers[1:]
		return "", err
	}
	f.answer("new-session", "", &ExitError{Command: "tmux new-session", Code: 1, Stderr: "duplicate session: engine"})
	if _, err := tm.Open(window(t)); err != nil {
		t.Fatal(err)
	}
	if d := f.dump(); !strings.Contains(d, "new-window") {
		t.Errorf("did not fall back to a new window:\n%s", d)
	}
}

func TestOpenReportsACreateFailureWhenNoSessionExists(t *testing.T) {
	tm, f := newFake(t)
	f.answer("new-session", "", &ExitError{Command: "tmux new-session", Code: 1, Stderr: "boom"})
	if _, err := tm.Open(window(t)); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("got %v", err)
	}
	if _, err := newFakeWith(t, "new-window", "", errors.New("x")).Open(window(t)); err == nil {
		t.Error("new-window failure was swallowed")
	}
}

func newFakeWith(t *testing.T, sub, out string, err error) *Tmux {
	t.Helper()
	tm, f := newFake(t)
	f.answer("has-session", "", nil)
	f.answer("list-windows", "@0 %0 0 other-1\n", nil)
	f.answer(sub, out, err)
	return tm
}

func TestOpenRejectsUnparsableIDs(t *testing.T) {
	tm, f := newFake(t)
	f.answer("new-session", "garbage\n", nil)
	if _, err := tm.Open(window(t)); err == nil || !strings.Contains(err.Error(), "garbage") {
		t.Errorf("got %v", err)
	}
}

func TestOpenKillsTheWindowWhenConfiguringFails(t *testing.T) {
	for _, sub := range []string{"set-option", "pipe-pane", "respawn-pane"} {
		tm, f := newFake(t)
		f.answer(sub, "", &ExitError{Command: "tmux " + sub, Code: 1, Stderr: "bad"})
		_, err := tm.Open(window(t))
		if err == nil || !strings.Contains(err.Error(), sub) {
			t.Errorf("%s: got %v", sub, err)
		}
		if d := f.dump(); !strings.HasSuffix(d, "tmux kill-window -t @0") {
			t.Errorf("%s: window left behind:\n%s", sub, d)
		}
	}
}

func TestOpenFailsWhenTmuxCannotRun(t *testing.T) {
	tm, f := newFake(t)
	f.answer("has-session", "", errors.New("executable file not found"))
	if _, err := tm.Open(window(t)); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("got %v", err)
	}
}

func TestOpenValidatesTheWindow(t *testing.T) {
	good := window(t)
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*Window){
		"name":             func(w *Window) { w.Name = "a#b" },
		"empty name":       func(w *Window) { w.Name = "" },
		"colon":            func(w *Window) { w.Name = "a:b" },
		"leading dash":     func(w *Window) { w.Name = "-a" },
		"relative dir":     func(w *Window) { w.Dir = "work" },
		"missing dir":      func(w *Window) { w.Dir = filepath.Join(w.Dir, "nope") },
		"dir is a file":    func(w *Window) { w.Dir = file },
		"empty command":    func(w *Window) { w.Command = " " },
		"relative log":     func(w *Window) { w.Log = "pane.log" },
		"log under a file": func(w *Window) { w.Log = filepath.Join(file, "x", "pane.log") },
	}
	for name, mutate := range cases {
		tm, f := newFake(t)
		w := good
		mutate(&w)
		if _, err := tm.Open(w); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if strings.Contains(f.dump(), "new-") {
			t.Errorf("%s: tmux was asked to create something", name)
		}
	}
	for _, name := range []string{"boga3-412", "next.js-9", "_x", "A1", ".github-5"} {
		tm, _ := newFake(t)
		w := good
		w.Name = name
		if _, err := tm.Open(w); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestSocketSelectsAPrivateServer(t *testing.T) {
	tm, f := newFake(t)
	tm.Socket = "private"
	if _, err := tm.HasSession(); err != nil && !errors.As(err, new(*ExitError)) {
		t.Fatal(err)
	}
	if got := f.calls[0]; got != "tmux -L private has-session -t =engine:" {
		t.Errorf("got %q", got)
	}
}

func TestHasSession(t *testing.T) {
	tm, f := newFake(t)
	if ok, err := tm.HasSession(); ok || err != nil {
		t.Errorf("no session: %v, %v", ok, err)
	}
	f.answer("has-session", "", nil)
	if ok, err := tm.HasSession(); !ok || err != nil {
		t.Errorf("session: %v, %v", ok, err)
	}
	f.answer("has-session", "", errors.New("not found"))
	if ok, err := tm.HasSession(); ok || err == nil {
		t.Errorf("tmux missing: %v, %v", ok, err)
	}
}

func TestWindowsAndHasWindow(t *testing.T) {
	tm, f := newFake(t)
	if got, err := tm.Windows(); got != nil || err != nil {
		t.Errorf("no session: %v, %v", got, err)
	}
	if ok, err := tm.HasWindow("a-1"); ok || err != nil {
		t.Errorf("no session: %v, %v", ok, err)
	}
	f.answer("has-session", "", nil)
	f.answer("list-windows", "@0 %0 0 boga3-412\n@3 %7 1 next.js-9\n", nil)
	got, err := tm.Windows()
	want := []WindowInfo{{Name: "boga3-412", ID: "@0", PaneID: "%0"}, {Name: "next.js-9", ID: "@3", PaneID: "%7", Dead: true}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, %v", got, err)
	}
	for name, exists := range map[string]bool{"boga3-412": true, "boga3": false, "boga3-4123": false, "next.js-9": true} {
		if ok, err := tm.HasWindow(name); ok != exists || err != nil {
			t.Errorf("HasWindow(%q) = %v, %v", name, ok, err)
		}
	}
	f.answer("list-windows", "garbage\n", nil)
	if _, err := tm.Windows(); err == nil || !strings.Contains(err.Error(), "garbage") {
		t.Errorf("got %v", err)
	}
	f.answer("list-windows", "", errors.New("gone"))
	if _, err := tm.Windows(); err == nil {
		t.Error("list-windows failure was swallowed")
	}
	if ok, err := tm.HasWindow("x"); ok || err == nil {
		t.Errorf("HasWindow hid the failure: %v, %v", ok, err)
	}
}

func TestEscapeArg(t *testing.T) {
	for in, want := range map[string]string{
		"echo hi":           "echo hi",
		"echo hi;":          `echo hi\;`,
		`find . -exec x \;`: `find . -exec x \\;`,
		";":                 `\;`,
		"a;b":               "a;b",
	} {
		if got := escapeArg(in); got != want {
			t.Errorf("escapeArg(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestShellQuoteAndFormatEscape(t *testing.T) {
	if got := shellQuote("it's"); got != `'it'\''s'` {
		t.Errorf("got %s", got)
	}
	if got := formatEscape("a#b#{c}"); got != "a##b##{c}" {
		t.Errorf("got %s", got)
	}
}

func TestExitErrorMessage(t *testing.T) {
	e := &ExitError{Command: "tmux x", Code: 2, Stderr: " oops \n"}
	if got := e.Error(); got != "tmux x: exit status 2: oops" {
		t.Errorf("got %q", got)
	}
	if got := (&ExitError{Command: "tmux x", Code: 1}).Error(); got != "tmux x: exit status 1" {
		t.Errorf("got %q", got)
	}
}

func TestExecReportsExitsAndMissingBinaries(t *testing.T) {
	out, err := Exec("sh", "-c", "echo out; echo err >&2; exit 3")
	var exit *ExitError
	if !errors.As(err, &exit) || exit.Code != 3 || strings.TrimSpace(exit.Stderr) != "err" || strings.TrimSpace(out) != "out" {
		t.Errorf("got %q, %v", out, err)
	}
	if out, err := Exec("sh", "-c", "echo fine"); err != nil || out != "fine\n" {
		t.Errorf("got %q, %v", out, err)
	}
	if _, err := Exec("engine-no-such-binary"); err == nil || errors.As(err, &exit) {
		t.Errorf("a missing binary is not an exit status: %v", err)
	}
}

func TestNewUsesTheDefaultServer(t *testing.T) {
	tm := New()
	if tm.Bin != "tmux" || tm.Socket != "" || tm.Run == nil {
		t.Errorf("got %+v", tm)
	}
}
