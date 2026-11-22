package session

import (
	"errors"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// scopedFake is newFake with t.Scope set, answering the scoped start with
// out and err.
func scopedFake(t *testing.T, out string, err error) (*Tmux, *fake) {
	t.Helper()
	tm, f := newFake(t)
	tm.Scope = ScopeCommand
	f.answer("--user", out, err)
	return tm, f
}

func TestOpenStartsTheSessionInItsOwnScope(t *testing.T) {
	tm, f := scopedFake(t, "@0 %0\n", nil)
	tm.Socket = "side"
	w := window(t)
	got, err := tm.Open(w)
	if err != nil || got.ScopeErr != nil || got.ID != "@0" {
		t.Fatalf("got %+v, %v", got, err)
	}
	want := "systemd-run --user --scope --quiet --collect --description=engine session tmux " +
		"tmux -L side new-session -d -P -F #{window_id} #{pane_id} -s engine -n boga3-412 -c " + w.Dir + " sleep 300"
	if f.calls[1] != want {
		t.Errorf("the start ran\n%s\nwant\n%s", f.calls[1], want)
	}
	for _, c := range f.calls[2:] {
		if strings.HasPrefix(c, "systemd-run") || strings.Contains(c, "new-session") {
			t.Errorf("only the session's start runs in the scope, and once: %s", c)
		}
	}
}

func TestOpenAddsAWindowOutsideTheScope(t *testing.T) {
	tm, f := scopedFake(t, "", errors.New("must not run"))
	f.answer("has-session", "", nil)
	f.answer("list-windows", "@0 %0 0 other-1\n", nil)
	if _, err := tm.Open(window(t)); err != nil {
		t.Fatal(err)
	}
	if d := f.dump(); strings.Contains(d, "systemd-run") || !strings.Contains(d, "new-window") {
		t.Errorf("a window in a running session needs no scope:\n%s", d)
	}
}

func TestOpenWithoutAScopeStartsTmuxDirectly(t *testing.T) {
	tm, f := newFake(t)
	got, err := tm.Open(window(t))
	if err != nil || got.ScopeErr != nil {
		t.Fatalf("got %+v, %v", got, err)
	}
	if d := f.dump(); strings.Contains(d, "systemd-run") || !strings.HasPrefix(f.calls[1], "tmux new-session") {
		t.Errorf("calls:\n%s", d)
	}
}

func TestOpenFallsBackWhenTheScopeIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"systemd-run missing", errors.New(`systemd-run: exec: "systemd-run": executable file not found in $PATH`)},
		{"no user bus", &ExitError{Command: "systemd-run --user --scope", Code: 1, Stderr: "Failed to connect to user scope bus via local transport"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tm, f := scopedFake(t, "", tc.err)
			got, err := tm.Open(window(t))
			if err != nil {
				t.Fatal(err)
			}
			if got.ScopeErr == nil || got.ScopeErr.Error() != tc.err.Error() || got.ID != "@0" {
				t.Errorf("the fallback is reported: %+v", got)
			}
			if !strings.HasPrefix(f.calls[1], "systemd-run ") || !strings.HasPrefix(f.calls[3], "tmux new-session") {
				t.Errorf("the plain start follows the refused one:\n%s", f.dump())
			}
		})
	}
}

func TestOpenReportsBothFailedStarts(t *testing.T) {
	tm, f := scopedFake(t, "", errors.New("no bus"))
	f.answer("new-session", "", &ExitError{Command: "tmux new-session", Code: 1, Stderr: "boom"})
	_, err := tm.Open(window(t))
	if err == nil || !strings.Contains(err.Error(), "boom") || !strings.Contains(err.Error(), "no bus") {
		t.Errorf("got %v", err)
	}
}

// A scoped start that lost a race to another process's start is no
// fallback: the window goes into the session that now exists.
func TestOpenSurvivesARaceWithTheScopedStart(t *testing.T) {
	tm, f := scopedFake(t, "", &ExitError{Command: "systemd-run", Code: 1, Stderr: "duplicate session: engine"})
	answers := []error{noSession, nil, nil}
	f.answers["has-session"] = func() (string, error) {
		err := answers[0]
		answers = answers[1:]
		return "", err
	}
	got, err := tm.Open(window(t))
	if err != nil || got.ScopeErr != nil || got.ID != "@1" {
		t.Fatalf("got %+v, %v", got, err)
	}
	if d := f.dump(); strings.Count(d, "new-session") != 1 {
		t.Errorf("the start ran once:\n%s", d)
	}
}

type dirInfo struct{ os.FileInfo }

func (dirInfo) IsDir() bool { return true }

type fileInfo struct{ os.FileInfo }

func (fileInfo) IsDir() bool { return false }

func TestHostScope(t *testing.T) {
	stat := func(info os.FileInfo, err error) func(string) (os.FileInfo, error) {
		return func(path string) (os.FileInfo, error) {
			if path != "/run/systemd/system" {
				t.Errorf("stat %s", path)
			}
			return info, err
		}
	}
	in := func(cgroup string, err error) func() (string, error) {
		return func() (string, error) { return cgroup, err }
	}
	service := in("0::/user.slice/user-1000.slice/user@1000.service/app.slice/engine-server.service\n", nil)
	for _, tc := range []struct {
		name   string
		goos   string
		stat   func(string) (os.FileInfo, error)
		cgroup func() (string, error)
		want   []string
	}{
		{"inside a service on systemd", "linux", stat(dirInfo{}, nil), service, ScopeCommand},
		{"an SSH login's session scope", "linux", stat(dirInfo{}, nil), in("0::/user.slice/user-1000.slice/session-579.scope\n", nil), nil},
		{"a tmux pane's scope", "linux", stat(dirInfo{}, nil), in("0::/user.slice/user-1000.slice/user@1000.service/app.slice/tmux-spawn-1.scope\n", nil), nil},
		{"an unreadable cgroup", "linux", stat(dirInfo{}, nil), in("", errors.New("no /proc")), nil},
		{"linux without systemd", "linux", stat(nil, os.ErrNotExist), service, nil},
		{"a file, not systemd's directory", "linux", stat(fileInfo{}, nil), service, nil},
		{"macOS", "darwin", stat(dirInfo{}, nil), service, nil},
	} {
		if got := hostScope(tc.goos, tc.stat, tc.cgroup); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %v", tc.name, got)
		}
	}
}

// New decides the scope for this process.
func TestNewTakesThisProcesssScope(t *testing.T) {
	if got, want := New().Scope, hostScope(runtime.GOOS, os.Stat, ownCgroup); !reflect.DeepEqual(got, want) {
		t.Errorf("New().Scope %v, want %v", got, want)
	}
	if runtime.GOOS == "linux" {
		if _, err := ownCgroup(); err != nil {
			t.Error(err)
		}
	}
}

func TestServiceUnit(t *testing.T) {
	for _, tc := range []struct{ name, cgroup, want string }{
		{"inside the server's unit", "0::/user.slice/user-1000.slice/user@1000.service/app.slice/engine-server.service\n", "engine-server.service"},
		{"a scope of its own", "0::/user.slice/user-1000.slice/user@1000.service/app.slice/run-p42-i43.scope\n", ""},
		{"a login session", "0::/user.slice/user-1000.slice/session-579.scope\n", ""},
		{"a service's delegated subgroup", "0::/system.slice/ci.service/payload\n", "ci.service"},
		{"a legacy host", "12:pids:/user.slice\n1:name=systemd:/system.slice/engine.service\n", "engine.service"},
		{"the unified line wins", "1:name=systemd:/system.slice/old.service\n0::/user.slice/session-2.scope\n", ""},
		{"outside systemd", "0::/\n", ""},
		{"no usable line", "garbage\n", ""},
		{"nothing", "", ""},
	} {
		if got := ServiceUnit(tc.cgroup); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

// stubSystemdRun writes a systemd-run that takes its options and execs the
// command after them, as systemd-run does once the scope exists.
func stubSystemdRun(t *testing.T) string {
	t.Helper()
	path := t.TempDir() + "/systemd-run"
	script := "#!/bin/sh\nwhile [ $# -gt 0 ]; do case $1 in -*) shift ;; *) break ;; esac; done\nexec \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRealScopedStartYieldsAWorkingSession(t *testing.T) {
	tm := privateTmux(t)
	tm.Scope = append([]string{stubSystemdRun(t)}, ScopeCommand[1:]...)
	run := tm.Run
	tm.Run = func(name string, args ...string) (string, error) {
		if name != tm.Scope[0] {
			return run(name, args...)
		}
		// The private server reads no tmux.conf: -f /dev/null goes after
		// the scope's options, tmux and its -L <socket>.
		at := len(tm.Scope) + 2
		return Exec(name, append(append(args[:at:at], "-f", "/dev/null"), args[at:]...)...)
	}
	dir := t.TempDir()
	w := Window{Name: "scoped-1", Dir: dir, Command: "echo scoped-ready; sleep 300", Log: dir + "/pane.log"}
	got, err := tm.Open(w)
	if err != nil || got.ScopeErr != nil {
		t.Fatalf("got %+v, %v", got, err)
	}
	eventually(t, "the window's output", func() bool { return strings.Contains(readFile(w.Log), "scoped-ready") })
	second, err := tm.Open(Window{Name: "scoped-2", Dir: dir, Command: "sleep 300"})
	if err != nil {
		t.Fatal(err)
	}
	windows, err := tm.Windows()
	if err != nil || len(windows) != 2 || windows[0].Name != "scoped-1" || windows[1].ID != second.ID {
		t.Errorf("windows %+v, %v", windows, err)
	}
}
