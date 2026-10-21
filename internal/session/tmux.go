// Package session is engine's local host layer. This file is the tmux layer:
// the session `engine`, its options, the windows that run tasks and the
// pane logs. tmux is reached only through its CLI, behind a Runner, so tests
// replace it or point it at a private socket.
package session

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// Name is the tmux session engine owns on every host.
const Name = "engine"

// holdCommand keeps a new window's pane alive while engine configures it.
// The real command replaces it with respawn-pane, so the window's options
// and its pane log are in place before the first byte of output.
const holdCommand = "sleep 300"

// sessionOptions are set on the session `engine` only. A window option must
// be set per window: the host's ~/.tmux.conf and the server's global options
// are never touched.
var sessionOptions = [][2]string{
	{"mouse", "on"},
	{"history-limit", "50000"},
}

var windowOptions = [][2]string{
	{"allow-rename", "off"},
	{"automatic-rename", "off"},
	{"window-size", "latest"},
	{"remain-on-exit", "on"},
}

// Runner runs name with args and returns its standard output. A command that
// exits non-zero fails with an *ExitError.
type Runner func(name string, args ...string) (string, error)

// ExitError is a command that ran and exited non-zero.
type ExitError struct {
	Command string
	Code    int
	Stderr  string
}

func (e *ExitError) Error() string {
	msg := fmt.Sprintf("%s: exit status %d", e.Command, e.Code)
	if s := strings.TrimSpace(e.Stderr); s != "" {
		msg += ": " + s
	}
	return msg
}

// Exec is the Runner that starts real processes.
func Exec(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return string(out), &ExitError{Command: name + " " + strings.Join(args, " "), Code: exit.ExitCode(), Stderr: stderr.String()}
	}
	if err != nil {
		return string(out), fmt.Errorf("%s: %w", name, err)
	}
	return string(out), nil
}

// Tmux drives one tmux server.
type Tmux struct {
	Run Runner
	// Bin is the tmux binary.
	Bin string
	// Socket names a private server (`tmux -L`). Empty is the host's default
	// server, where engine's session lives in production.
	Socket string
	// Awake is the caffeinate binary that keeps a macOS host awake while a
	// window's command runs. Empty turns it off.
	Awake string
}

// New returns the Tmux for this host: the default server, and caffeinate on
// macOS.
func New() *Tmux {
	t := &Tmux{Run: Exec, Bin: "tmux"}
	if runtime.GOOS == "darwin" {
		t.Awake = "/usr/bin/caffeinate"
	}
	return t
}

// Window is a window to open.
type Window struct {
	// Name is the window's name, `<repo>-<task>`.
	Name string
	// Dir is the absolute directory the command runs in.
	Dir string
	// Command is the shell command the window runs.
	Command string
	// Log is the absolute path of the pane log. Empty means no log. Its
	// directory is created.
	Log string
}

// WindowInfo is a window of the session.
type WindowInfo struct {
	Name   string
	ID     string // tmux's window id, such as @3: a target that needs no escaping
	PaneID string // the window's pane, such as %5
	Dead   bool   // the command exited and the pane is kept for reading
}

// Opened is the result of Open.
type Opened struct {
	WindowInfo
	// AwakeErr is why tmux refused to start the keep-awake job. The window
	// is open regardless. A job that fails later is not reported.
	AwakeErr error
}

var (
	nameRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)
	idRe   = regexp.MustCompile(`^(@\d+) (%\d+)$`)
)

func (t *Tmux) tmux(args ...string) (string, error) {
	var full []string
	if t.Socket != "" {
		full = append(full, "-L", t.Socket)
	}
	for _, a := range args {
		full = append(full, escapeArg(a))
	}
	return t.Run(t.Bin, full...)
}

// escapeArg keeps tmux from reading a trailing `;` as a command separator.
func escapeArg(a string) string {
	if strings.HasSuffix(a, ";") {
		return a[:len(a)-1] + `\;`
	}
	return a
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// formatEscape protects text in a tmux argument that tmux expands as a format.
func formatEscape(s string) string {
	return strings.ReplaceAll(s, "#", "##")
}

// sessionTarget names the session `engine`, exactly.
const sessionTarget = "=" + Name + ":"

// HasSession reports whether the session `engine` exists. No server running
// means it does not; a tmux that cannot run is an error.
func (t *Tmux) HasSession() (bool, error) {
	_, err := t.tmux("has-session", "-t", sessionTarget)
	var exit *ExitError
	if errors.As(err, &exit) {
		return false, nil
	}
	return err == nil, err
}

// Windows lists the session's windows, none when the session does not exist.
func (t *Tmux) Windows() ([]WindowInfo, error) {
	ok, err := t.HasSession()
	if err != nil || !ok {
		return nil, err
	}
	out, err := t.tmux("list-windows", "-t", sessionTarget, "-F", "#{window_id} #{pane_id} #{pane_dead} #{window_name}")
	if err != nil {
		return nil, err
	}
	var windows []WindowInfo
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		f := strings.SplitN(line, " ", 4)
		if len(f) != 4 {
			return nil, fmt.Errorf("tmux list-windows: unexpected line %q", line)
		}
		windows = append(windows, WindowInfo{ID: f[0], PaneID: f[1], Dead: f[2] == "1", Name: f[3]})
	}
	return windows, nil
}

// HasWindow reports whether the session has a window named exactly name.
func (t *Tmux) HasWindow(name string) (bool, error) {
	windows, err := t.Windows()
	for _, w := range windows {
		if w.Name == name {
			return true, nil
		}
	}
	return false, err
}

func (w Window) validate() error {
	switch {
	case !nameRe.MatchString(w.Name):
		return fmt.Errorf("window name %q: use letters, digits, _, . and -, starting with a letter, digit or _", w.Name)
	case !filepath.IsAbs(w.Dir):
		return fmt.Errorf("window %s: directory %q is not absolute", w.Name, w.Dir)
	case strings.TrimSpace(w.Command) == "":
		return fmt.Errorf("window %s: no command", w.Name)
	case w.Log != "" && !filepath.IsAbs(w.Log):
		return fmt.Errorf("window %s: log %q is not absolute", w.Name, w.Log)
	}
	if info, err := os.Stat(w.Dir); err != nil || !info.IsDir() {
		return fmt.Errorf("window %s: %s is not a directory", w.Name, w.Dir)
	}
	return nil
}

// Open creates the session `engine` if it does not exist, sets engine's
// options, and opens a window that runs w.Command in w.Dir. The window keeps
// its last screen when the command exits, and its output is copied to w.Log
// from the first byte. It refuses a name the session already uses.
func (t *Tmux) Open(w Window) (Opened, error) {
	if err := w.validate(); err != nil {
		return Opened{}, err
	}
	windows, err := t.Windows()
	if err != nil {
		return Opened{}, err
	}
	for _, existing := range windows {
		if existing.Name == w.Name {
			return Opened{}, fmt.Errorf("window %s already exists in the tmux session %s", w.Name, Name)
		}
	}
	if w.Log != "" {
		if err := os.MkdirAll(filepath.Dir(w.Log), 0o755); err != nil {
			return Opened{}, err
		}
	}
	info, err := t.create(w, len(windows) > 0)
	if err != nil {
		return Opened{}, err
	}
	if err := t.configure(info, w); err != nil {
		_, _ = t.tmux("kill-window", "-t", info.ID) // best effort: report the first failure
		return Opened{}, err
	}
	return Opened{WindowInfo: info, AwakeErr: t.keepAwake(info.PaneID)}, nil
}

// create starts the window running holdCommand, creating the session with it
// when the session does not exist yet (a session always has a window).
func (t *Tmux) create(w Window, sessionExists bool) (WindowInfo, error) {
	format := "#{window_id} #{pane_id}"
	dir := formatEscape(w.Dir)
	if !sessionExists {
		out, err := t.tmux("new-session", "-d", "-P", "-F", format, "-s", Name, "-n", w.Name, "-c", dir, holdCommand)
		if err == nil {
			return parseIDs(out, w.Name)
		}
		// A concurrent start may have created the session first.
		if again, herr := t.HasSession(); herr != nil || !again {
			return WindowInfo{}, err
		}
	}
	out, err := t.tmux("new-window", "-d", "-P", "-F", format, "-t", sessionTarget, "-n", w.Name, "-c", dir, holdCommand)
	if err != nil {
		return WindowInfo{}, err
	}
	return parseIDs(out, w.Name)
}

func parseIDs(out, name string) (WindowInfo, error) {
	m := idRe.FindStringSubmatch(strings.TrimSpace(out))
	if m == nil {
		return WindowInfo{}, fmt.Errorf("tmux printed %q, want a window id and a pane id", strings.TrimSpace(out))
	}
	return WindowInfo{Name: name, ID: m[1], PaneID: m[2]}, nil
}

// configure applies engine's options and the pane log, then replaces the
// holding command with the real one.
func (t *Tmux) configure(info WindowInfo, w Window) error {
	for _, o := range sessionOptions {
		if _, err := t.tmux("set-option", "-t", sessionTarget, o[0], o[1]); err != nil {
			return err
		}
	}
	for _, o := range windowOptions {
		if _, err := t.tmux("set-option", "-w", "-t", info.ID, o[0], o[1]); err != nil {
			return err
		}
	}
	if w.Log != "" {
		if _, err := t.tmux("pipe-pane", "-t", info.PaneID, formatEscape("cat >> "+shellQuote(w.Log))); err != nil {
			return err
		}
	}
	_, err := t.tmux("respawn-pane", "-k", "-t", info.PaneID, "-c", formatEscape(w.Dir), w.Command)
	return err
}
