package session

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// privateTmux returns a Tmux on a socket of its own, and kills that server
// when the test ends. It never touches the default server, where the
// operator's real `engine` session lives. The test skips without tmux.
func privateTmux(t *testing.T) *Tmux {
	t.Helper()
	bin, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux is not installed")
	}
	// A shell inside tmux would otherwise point tmux at the operator's server.
	if v, ok := os.LookupEnv("TMUX"); ok {
		t.Setenv("TMUX", v)
		if err := os.Unsetenv("TMUX"); err != nil {
			t.Fatal(err)
		}
	}
	socket := fmt.Sprintf("engine-test-%d-%d", os.Getpid(), time.Now().UnixNano())
	tm := &Tmux{Run: Exec, Bin: bin, Socket: socket}
	t.Cleanup(func() {
		path, _ := tm.tmux("display-message", "-p", "#{socket_path}")
		_, _ = tm.tmux("kill-server")
		// kill-server leaves the socket file behind.
		if path = strings.TrimSpace(path); strings.Contains(path, socket) {
			_ = os.Remove(path)
		}
	})
	return tm
}

// eventually polls for something another process does asynchronously, such
// as a pane log filling. It asserts on the content, never on a duration; the
// deadline only ends a test whose condition can never hold.
func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("gave up waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func readFile(path string) string {
	data, _ := os.ReadFile(path)
	return string(data)
}

func show(t *testing.T, tm *Tmux, args ...string) string {
	t.Helper()
	out, err := tm.tmux(args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRealSessionWindowAndPaneLog(t *testing.T) {
	tm := privateTmux(t)
	if ok, err := tm.HasSession(); ok || err != nil {
		t.Fatalf("a fresh server has no session: %v, %v", ok, err)
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "runs", "r1", "pane.log")
	got, err := tm.Open(Window{Name: "boga3-412", Dir: dir, Command: "pwd; echo started; sleep 60", Log: log})
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := tm.HasSession(); !ok || err != nil {
		t.Fatalf("session: %v, %v", ok, err)
	}
	if ok, err := tm.HasWindow("boga3-412"); !ok || err != nil {
		t.Errorf("window: %v, %v", ok, err)
	}
	if ok, _ := tm.HasWindow("boga3"); ok {
		t.Error("a prefix is not a window")
	}
	windows, err := tm.Windows()
	if err != nil || len(windows) != 1 || windows[0] != got.WindowInfo || windows[0].Dead {
		t.Errorf("windows = %+v, %v", windows, err)
	}
	// The command's own output; pwd proves the directory (macOS temp
	// directories are symlinks, so either spelling counts).
	eventually(t, "the pane log", func() bool { return strings.Contains(readFile(log), "started") })
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if text := readFile(log); !strings.Contains(text, dir) && !strings.Contains(text, resolved) {
		t.Errorf("the command did not run in %s:\n%s", dir, text)
	}
}

func TestRealOptions(t *testing.T) {
	tm := privateTmux(t)
	dir := t.TempDir()
	got, err := tm.Open(Window{Name: "a-1", Dir: dir, Command: "sleep 60"})
	if err != nil {
		t.Fatal(err)
	}
	sess := show(t, tm, "show-options", "-t", sessionTarget)
	for _, want := range []string{"mouse on", "history-limit 50000"} {
		if !strings.Contains(sess, want) {
			t.Errorf("session options lack %q:\n%s", want, sess)
		}
	}
	win := show(t, tm, "show-options", "-w", "-t", got.ID)
	for _, want := range []string{"allow-rename off", "automatic-rename off", "window-size latest", "remain-on-exit on"} {
		if !strings.Contains(win, want) {
			t.Errorf("window options lack %q:\n%s", want, win)
		}
	}
	// A second window gets the same options, and the server's global ones stay.
	second, err := tm.Open(Window{Name: "next.js-9", Dir: dir, Command: "sleep 60"})
	if err != nil {
		t.Fatal(err)
	}
	if win := show(t, tm, "show-options", "-w", "-t", second.ID); !strings.Contains(win, "remain-on-exit on") {
		t.Errorf("second window options:\n%s", win)
	}
	if g := show(t, tm, "show-options", "-g", "remain-on-exit"); !strings.Contains(g, "remain-on-exit off") {
		t.Errorf("the global option changed: %s", g)
	}
	names := []string{}
	windows, _ := tm.Windows()
	for _, w := range windows {
		names = append(names, w.Name)
	}
	if strings.Join(names, ",") != "a-1,next.js-9" {
		t.Errorf("windows = %v: the holding command must leave no extra window", names)
	}
	if _, err := tm.Open(Window{Name: "a-1", Dir: dir, Command: "true"}); err == nil {
		t.Error("a second window with the same name was opened")
	}
}

func TestRealCrashedCommandKeepsItsScreenAndLog(t *testing.T) {
	tm := privateTmux(t)
	dir := t.TempDir()
	log := filepath.Join(dir, "pane.log")
	if _, err := tm.Open(Window{Name: "crash-1", Dir: dir, Command: "echo boom; exit 3", Log: log}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the window to die", func() bool {
		w, err := tm.Windows()
		return err == nil && len(w) == 1 && w[0].Dead
	})
	eventually(t, "the log", func() bool { return strings.Contains(readFile(log), "boom") })
	// The pane stays, and tmux appends its "Pane is dead" line to what was on it.
	screen := show(t, tm, "capture-pane", "-p", "-S", "-", "-t", "=engine:crash-1")
	if !strings.Contains(screen, "boom") || !strings.Contains(screen, "Pane is dead (status 3") {
		t.Errorf("last screen lost:\n%s", screen)
	}
}

func TestRealCommandsAreTypedLiterally(t *testing.T) {
	tm := privateTmux(t)
	dir := t.TempDir()
	log := filepath.Join(dir, "pane.log")
	// A trailing semicolon, a backslash-semicolon, quotes and format syntax.
	command := `echo "#{pane_id} ##"; echo done; echo x\;`
	if _, err := tm.Open(Window{Name: "lit-1", Dir: dir, Command: command, Log: log}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the log", func() bool { return strings.Contains(readFile(log), "x;") })
	text := readFile(log)
	for _, want := range []string{"done", "x;", "#{pane_id} ##"} {
		if !strings.Contains(text, want) {
			t.Errorf("log lacks %q:\n%s", want, text)
		}
	}
}

func TestRealDirectoryAndLogPathsWithSpecialCharacters(t *testing.T) {
	tm := privateTmux(t)
	dir := filepath.Join(t.TempDir(), "it's #x {x}")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "p'ane #{pane_id}.log")
	if _, err := tm.Open(Window{Name: "odd-1", Dir: dir, Command: "pwd; echo ok", Log: log}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the log", func() bool { return strings.Contains(readFile(log), "ok") })
	if text := readFile(log); !strings.Contains(text, "it's #x {x}") {
		t.Errorf("the command ran in the wrong directory:\n%s", text)
	}
}

func TestRealKeepAwakeRunsCaffeinateOnThePanePid(t *testing.T) {
	tm := privateTmux(t)
	dir := t.TempDir()
	// A stand-in that records what it was started with. Never the real one.
	record := filepath.Join(dir, "awake.txt")
	fakeBin := filepath.Join(dir, "fake caffeinate")
	script := "#!/bin/sh\necho \"$@\" > '" + record + "'\n"
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	tm.Awake = fakeBin
	got, err := tm.Open(Window{Name: "awake-1", Dir: dir, Command: "sleep 60"})
	if err != nil || got.AwakeErr != nil {
		t.Fatalf("got %+v, %v", got, err)
	}
	eventually(t, "the stand-in to run", func() bool { return strings.HasSuffix(readFile(record), "\n") })
	pid := strings.TrimSpace(show(t, tm, "display-message", "-p", "-t", got.PaneID, "#{pane_pid}"))
	if want := "-s -w " + pid + "\n"; readFile(record) != want {
		t.Errorf("caffeinate got %q, want %q", readFile(record), want)
	}
}
